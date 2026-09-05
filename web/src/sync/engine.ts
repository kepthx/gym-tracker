import {
  allPrograms,
  applyRemote,
  deadLetters,
  dropFromOutbox,
  getMeta,
  moveToDeadLetter,
  outboxHead,
  outboxSize,
  setMeta,
} from '../db/idb'
import { mergeSession, mergeSet } from '../db/merge'
import type { OpResult } from '../types'
import { ApiError, getSync, OfflineError, postSync } from './client'

/**
 * Save state. The indicator shows the WORST of the current ones.
 *
 * The key distinction is between `local` and `error`. A queue at the gym with no signal is
 * an expected state, not a failure: it shows in amber and reads as success. If it looked
 * alarming, people would stop believing the indicator, and the "visible save status"
 * requirement would die along with that trust.
 */
export type SyncState = 'synced' | 'local' | 'syncing' | 'error' | 'auth' | 'degraded'

export interface SyncStatus {
  state: SyncState
  /** How many actions are waiting to be sent. The number matters more than an icon: it
   *  is what people trust. */
  pending: number
  /** Operations the server rejected. They need attention and do not clear themselves. */
  dead: number
  lastSyncAt: number | null
  /** Skew between the device clock and the server, in ms. */
  clockSkew: number
}

const BATCH_LIMIT = 200
const DEBOUNCE_MS = 400
const POLL_MS = 15_000
const BACKOFF_MS = [1000, 2000, 4000, 8000, 16000, 30000]
const SERVER_FAILURES_BEFORE_ALARM = 3

type Listener = (status: SyncStatus) => void

export class SyncEngine {
  private deviceID = ''
  private status: SyncStatus = {
    state: 'synced',
    pending: 0,
    dead: 0,
    lastSyncAt: null,
    clockSkew: 0,
  }
  private listeners = new Set<Listener>()
  private inFlight = false
  /** A flush was asked for while one was running: run again as soon as it finishes. */
  private dirty = false
  /**
   * How many operations go in one request. Normally BATCH_LIMIT; halved after the server
   * refuses a whole batch with a 4xx, so the one operation it objects to can be isolated
   * and dead-lettered instead of jamming everything behind it.
   */
  private batchLimit = BATCH_LIMIT
  private debounceTimer: ReturnType<typeof setTimeout> | null = null
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  private pollTimer: ReturnType<typeof setInterval> | null = null
  private serverFailures = 0
  private backoffStep = 0
  private onChanged: () => void = () => {}

  /** Called after every data apply so the screen rereads from storage. */
  setOnChanged(fn: () => void): void {
    this.onChanged = fn
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener)
    listener(this.status)
    return () => this.listeners.delete(listener)
  }

  getStatus(): SyncStatus {
    return this.status
  }

  async init(): Promise<void> {
    let id = await getMeta<string>('device_id')
    if (!id) {
      id = crypto.randomUUID()
      await setMeta('device_id', id)
    }
    this.deviceID = id

    await this.refreshCounts()
    this.installTriggers()
    this.schedule(0)
  }

  markDegraded(): void {
    this.patch({ state: 'degraded' })
  }

  /**
   * Send triggers. Background Sync does not exist on iOS, so all of them fire from the
   * active page. Syncing in a service worker would gain nothing and would add a second
   * copy of the state.
   */
  private installTriggers(): void {
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') {
          // Returning to the app resets the backoff: the user may have just walked back
          // into coverage, and there is no reason to make them wait half a minute.
          this.backoffStep = 0
          this.schedule(0)
        }
      })
    }
    if (typeof window !== 'undefined') {
      window.addEventListener('online', () => {
        this.backoffStep = 0
        this.schedule(0)
      })
    }
    this.pollTimer = setInterval(() => {
      if (this.status.pending > 0) this.schedule(0)
    }, POLL_MS)
  }

  stop(): void {
    if (this.pollTimer) clearInterval(this.pollTimer)
    if (this.debounceTimer) clearTimeout(this.debounceTimer)
    if (this.retryTimer) clearTimeout(this.retryTimer)
  }

  /** Schedules a send. The debounce glues a series of taps into one batch. */
  schedule(delay = DEBOUNCE_MS): void {
    if (this.debounceTimer) clearTimeout(this.debounceTimer)
    this.debounceTimer = setTimeout(() => {
      this.debounceTimer = null
      void this.flush()
    }, delay)
  }

  /** Manual retry from the button in the red banner. */
  retryNow(): void {
    this.backoffStep = 0
    this.serverFailures = 0
    this.schedule(0)
  }

  /**
   * Drains the queue. A call that lands while a flush is running is not dropped: it marks
   * the engine dirty, and the running flush goes round once more when it finishes.
   * Otherwise a set tapped during a sync would wait for the 15-second poll.
   */
  async flush(): Promise<void> {
    if (this.inFlight) {
      this.dirty = true
      return
    }
    this.inFlight = true
    try {
      do {
        this.dirty = false
        await this.flushOnce()
      } while (this.dirty)
    } finally {
      this.inFlight = false
    }
  }

  /** Rereads the queue and dead-letter counts — after the user dismissed a rejection, say. */
  async recount(): Promise<void> {
    await this.refreshCounts()
  }

  private async flushOnce(): Promise<void> {
    const pending = await outboxHead(this.batchLimit)
    const since = (await getMeta<number>('cursor')) ?? 0
    const known = (await allPrograms()).map((p) => p.hash)

    if (pending.length > 0) this.patch({ state: 'syncing' })

    try {
      const response =
        pending.length > 0
          ? await postSync({
              device_id: this.deviceID,
              since,
              ops: pending.map((e) => e.op),
              known_programs: known,
            })
          : await getSync(since, known)

      await applyRemote({
        sessions: response.changes.sessions,
        sets: response.changes.sets,
        programs: response.changes.programs,
        cursor: response.cursor,
        mergeSession,
        mergeSet,
      })

      if (pending.length > 0) await this.settle(pending, response.results)

      this.serverFailures = 0
      this.backoffStep = 0
      if (this.batchLimit < BATCH_LIMIT) {
        // A narrowed batch went through: the rest of the queue is still waiting behind
        // it and must not wait for the poll. A full-width batch does not loop here — an
        // operation the server leaves without a verdict would otherwise spin forever.
        this.batchLimit = BATCH_LIMIT
        this.dirty = true
      }
      await setMeta('clock_skew', Date.now() - response.server_time)

      // A successful sync clears the blocking states. Without this the red "login required"
      // banner would hang forever after logging back in: refreshCounts deliberately leaves
      // those states alone so they do not flicker between attempts.
      const blocking = this.status.state === 'auth' || this.status.state === 'error'
      this.patch({
        lastSyncAt: Date.now(),
        clockSkew: Date.now() - response.server_time,
        ...(blocking ? { state: 'syncing' as const } : null),
      })
      await this.refreshCounts()
      this.onChanged()

      // The server truncated the response at the limit — go straight for the next page.
      if (response.has_more) this.schedule(0)
    } catch (err) {
      await this.handleFailure(err, pending)
    }
  }

  /**
   * Processes the server's per-operation verdicts.
   *
   * Rejected ones move to a separate store and leave the queue: keeping them in it would
   * jam it forever and lose everything behind them along with it.
   */
  private async settle(
    pending: { seq: number; op: { op_id: string } }[],
    results: OpResult[],
  ): Promise<void> {
    const verdict = new Map(results.map((r) => [r.op_id, r]))
    const settledSeqs: number[] = []
    const rejected: { seq: number; op: never; reason: string }[] = []

    for (const entry of pending) {
      const result = verdict.get(entry.op.op_id)
      if (!result) continue // no verdict for this one — leave it in the queue
      if (result.status === 'rejected') {
        rejected.push({
          seq: entry.seq,
          op: entry.op as never,
          reason: result.reason ?? 'операция отклонена',
        })
      } else {
        settledSeqs.push(entry.seq)
      }
    }

    await dropFromOutbox(settledSeqs)
    await moveToDeadLetter(rejected, Date.now())
  }

  private async handleFailure(
    err: unknown,
    pending: { seq: number; op: { op_id: string } }[],
  ): Promise<void> {
    await this.refreshCounts()

    if (err instanceof OfflineError) {
      // No connection. The data is on the device, and at the gym that is a normal state —
      // no red, just a count of what is unsent.
      this.patch({ state: this.status.pending > 0 ? 'local' : 'synced' })
      this.scheduleRetry()
      return
    }

    if (err instanceof ApiError && err.status === 401) {
      // The queue is NOT cleared: a workout is sitting in it, and losing that is
      // unacceptable. The user will log back in and everything will get through.
      this.patch({ state: 'auth' })
      return
    }

    if (err instanceof ApiError && err.retriable) {
      this.serverFailures += 1
      this.patch({
        state: this.serverFailures >= SERVER_FAILURES_BEFORE_ALARM ? 'error' : 'local',
      })
      this.scheduleRetry(err.retryAfter > 0 ? err.retryAfter * 1000 : undefined)
      return
    }

    if (err instanceof ApiError && pending.length > 0) {
      // The server refused the whole batch — a 4xx. Retrying it verbatim would fail the
      // same way forever and jam everything behind it, so partial failability has to be
      // recovered by hand: narrow the batch until the offending operation stands alone,
      // then dead-letter it with the server's reason and carry on with the rest.
      if (pending.length > 1) {
        this.batchLimit = Math.max(1, Math.floor(pending.length / 2))
        this.patch({ state: 'syncing' })
        this.dirty = true
        return
      }
      const only = pending[0]!
      await moveToDeadLetter(
        [{ seq: only.seq, op: only.op as never, reason: err.message }],
        Date.now(),
      )
      this.batchLimit = BATCH_LIMIT
      await this.refreshCounts()
      this.dirty = true
      return
    }

    // Everything else is a failure a retry will not cure. Show it plainly.
    this.patch({ state: 'error' })
  }

  private scheduleRetry(explicitDelay?: number): void {
    const base = explicitDelay ?? BACKOFF_MS[Math.min(this.backoffStep, BACKOFF_MS.length - 1)]!
    this.backoffStep = Math.min(this.backoffStep + 1, BACKOFF_MS.length - 1)

    const jitter = base * 0.2 * (Math.random() * 2 - 1)
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = setTimeout(() => void this.flush(), Math.max(500, base + jitter))
  }

  private async refreshCounts(): Promise<void> {
    const [pending, dead] = await Promise.all([outboxSize(), deadLetters()])
    const next: Partial<SyncStatus> = { pending, dead: dead.length }

    if (dead.length > 0) {
      next.state = 'error'
    } else if (this.status.state !== 'auth' && this.status.state !== 'degraded') {
      if (this.status.state !== 'error' || pending === 0) {
        next.state = pending > 0 ? 'local' : 'synced'
      }
    }
    this.patch(next)
  }

  private patch(next: Partial<SyncStatus>): void {
    this.status = { ...this.status, ...next }
    for (const listener of this.listeners) listener(this.status)
  }
}

export const engine = new SyncEngine()
