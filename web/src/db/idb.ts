import type { DeadLetter, Op, Program, SessionRow, SetRow } from '../types'

/**
 * IndexedDB is the source of truth for rendering.
 *
 * The UI reads only from here and never straight from a network response: server responses
 * are merged into the database, and the screen redraws from the database. It follows from
 * that rule that there is no "offline mode" — the app is always offline-first, and the
 * network is a background reconciler. One code path instead of two.
 *
 * This is not a luxury: iOS restarts a home-screen app on very nearly every return to it,
 * so a cold start has to render from local data, often with no connection at all.
 */

const DB_NAME = 'gymtracker'
const DB_VERSION = 1
const OPEN_TIMEOUT_MS = 3000

export const STORE = {
  meta: 'meta',
  programs: 'programs',
  sessions: 'sessions',
  sets: 'sets',
  outbox: 'outbox',
  deadletter: 'deadletter',
} as const

export type MetaKey =
  | 'device_id'
  | 'cursor'
  | 'token'
  | 'screen'
  | 'active_session'
  | 'persisted'
  | 'user'
  | 'seq'
  | 'clock_skew'
  | 'current_program'

let dbPromise: Promise<IDBDatabase> | null = null

/** Opens the database. Repeat calls hand back the same connection. */
export function openDB(): Promise<IDBDatabase> {
  if (!dbPromise) dbPromise = openWithTimeout()
  return dbPromise
}

/**
 * On some iOS builds, opening IndexedDB would hang forever. Racing it against a timeout
 * turns total data loss into visible degradation: the app says storage is unavailable and
 * asks the user not to close the tab.
 */
function openWithTimeout(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    let settled = false
    const timer = setTimeout(() => {
      if (!settled) {
        settled = true
        reject(new Error('хранилище не открылось за отведённое время'))
      }
    }, OPEN_TIMEOUT_MS)

    let request: IDBOpenDBRequest
    try {
      request = indexedDB.open(DB_NAME, DB_VERSION)
    } catch (err) {
      clearTimeout(timer)
      reject(err)
      return
    }

    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains(STORE.meta)) db.createObjectStore(STORE.meta)
      if (!db.objectStoreNames.contains(STORE.programs)) {
        db.createObjectStore(STORE.programs, { keyPath: 'hash' })
      }
      if (!db.objectStoreNames.contains(STORE.sessions)) {
        db.createObjectStore(STORE.sessions, { keyPath: 'id' })
      }
      if (!db.objectStoreNames.contains(STORE.sets)) {
        const sets = db.createObjectStore(STORE.sets, {
          keyPath: ['session_id', 'exercise_id', 'idx'],
        })
        sets.createIndex('by_session', 'session_id')
      }
      if (!db.objectStoreNames.contains(STORE.outbox)) {
        // autoIncrement fixes the queue order: it drains strictly by ascending key.
        const outbox = db.createObjectStore(STORE.outbox, { keyPath: 'seq', autoIncrement: true })
        outbox.createIndex('by_op_id', 'op.op_id', { unique: true })
      }
      if (!db.objectStoreNames.contains(STORE.deadletter)) {
        db.createObjectStore(STORE.deadletter, { keyPath: 'op_id' })
      }
    }

    request.onsuccess = () => {
      if (settled) {
        request.result.close()
        return
      }
      settled = true
      clearTimeout(timer)
      resolve(request.result)
    }
    request.onerror = () => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      reject(request.error ?? new Error('не удалось открыть хранилище'))
    }
  })
}

/** Waits for a transaction to finish. The promise resolves only after the commit. */
export function done(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error ?? new Error('транзакция не удалась'))
    tx.onabort = () => reject(tx.error ?? new Error('транзакция отменена'))
  })
}

function wrap<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('запрос к хранилищу не удался'))
  })
}

export async function getMeta<T>(key: MetaKey): Promise<T | undefined> {
  const db = await openDB()
  const tx = db.transaction(STORE.meta, 'readonly')
  return wrap<T | undefined>(tx.objectStore(STORE.meta).get(key))
}

export async function setMeta(key: MetaKey, value: unknown): Promise<void> {
  const db = await openDB()
  const tx = db.transaction(STORE.meta, 'readwrite')
  tx.objectStore(STORE.meta).put(value, key)
  await done(tx)
}

export async function allSessions(): Promise<SessionRow[]> {
  const db = await openDB()
  const tx = db.transaction(STORE.sessions, 'readonly')
  return wrap<SessionRow[]>(tx.objectStore(STORE.sessions).getAll())
}

export async function allSets(): Promise<SetRow[]> {
  const db = await openDB()
  const tx = db.transaction(STORE.sets, 'readonly')
  return wrap<SetRow[]>(tx.objectStore(STORE.sets).getAll())
}

export async function allPrograms(): Promise<{ hash: string; json: Program }[]> {
  const db = await openDB()
  const tx = db.transaction(STORE.programs, 'readonly')
  return wrap(tx.objectStore(STORE.programs).getAll())
}

export async function outboxSize(): Promise<number> {
  const db = await openDB()
  const tx = db.transaction(STORE.outbox, 'readonly')
  return wrap<number>(tx.objectStore(STORE.outbox).count())
}

export async function outboxHead(limit: number): Promise<{ seq: number; op: Op }[]> {
  const db = await openDB()
  const tx = db.transaction(STORE.outbox, 'readonly')
  return wrap(tx.objectStore(STORE.outbox).getAll(undefined, limit))
}

export async function deadLetters(): Promise<DeadLetter[]> {
  const db = await openDB()
  const tx = db.transaction(STORE.deadletter, 'readonly')
  return wrap<DeadLetter[]>(tx.objectStore(STORE.deadletter).getAll())
}

/**
 * Applies a user action: writes the new state AND enqueues the operation in ONE
 * transaction.
 *
 * This is the client's central invariant. If state and queue can diverge, the result is
 * either a lost write (a set was marked, never reached the server, and is not there
 * locally) or a phantom sync (something the user never did reached the server). A
 * multi-store transaction gives atomicity for free.
 *
 * The UI redraws only after the promise resolves: showing the checkmark before the commit
 * means lying to the user about what is saved.
 */
export async function applyLocal(
  op: Op,
  write: (stores: {
    sessions: IDBObjectStore
    sets: IDBObjectStore
  }) => void,
): Promise<void> {
  const db = await openDB()
  const tx = db.transaction([STORE.sessions, STORE.sets, STORE.outbox], 'readwrite')

  try {
    write({
      sessions: tx.objectStore(STORE.sessions),
      sets: tx.objectStore(STORE.sets),
    })
    tx.objectStore(STORE.outbox).add({ op })
  } catch (err) {
    // The transaction does not abort on its own: the writes already issued would commit
    // happily and the state would diverge from the queue. That is exactly the path where a
    // set looks saved but never reaches the server.
    try {
      tx.abort()
    } catch {
      // Already aborted by the failing request itself — no need to abort twice.
    }
    throw err
  }

  await done(tx)
}

/**
 * Writes the delta received from the server and advances the cursor in one transaction.
 * Otherwise a crash mid-apply would skip some rows forever: the cursor would move ahead
 * while the rows never landed.
 */
export async function applyRemote(params: {
  sessions: SessionRow[]
  sets: SetRow[]
  programs: { hash: string; json: Program }[]
  cursor: number
  mergeSession: (current: SessionRow | undefined, incoming: SessionRow) => SessionRow
  mergeSet: (current: SetRow | undefined, incoming: SetRow) => SetRow
}): Promise<void> {
  const db = await openDB()
  const tx = db.transaction(
    [STORE.sessions, STORE.sets, STORE.programs, STORE.meta],
    'readwrite',
  )

  // A promise must not be awaited inside a transaction: as soon as the microtask queue
  // drains with no outstanding requests, IndexedDB closes the transaction and the next
  // write fails with "transaction is not active". So the merge happens in the read's own
  // onsuccess handler — that keeps the transaction alive until the last write.
  const sessions = tx.objectStore(STORE.sessions)
  for (const incoming of params.sessions) {
    const read = sessions.get(incoming.id)
    read.onsuccess = () => {
      sessions.put(params.mergeSession(read.result as SessionRow | undefined, incoming))
    }
  }

  const sets = tx.objectStore(STORE.sets)
  for (const incoming of params.sets) {
    const read = sets.get([incoming.session_id, incoming.exercise_id, incoming.idx])
    read.onsuccess = () => {
      sets.put(params.mergeSet(read.result as SetRow | undefined, incoming))
    }
  }

  // Program snapshots are immutable — written once and kept forever.
  const programs = tx.objectStore(STORE.programs)
  for (const p of params.programs) programs.put(p)

  tx.objectStore(STORE.meta).put(params.cursor, 'cursor')
  await done(tx)
}

/** Removes from the queue the operations whose fate the server has already decided. */
export async function dropFromOutbox(seqs: number[]): Promise<void> {
  if (seqs.length === 0) return
  const db = await openDB()
  const tx = db.transaction(STORE.outbox, 'readwrite')
  const outbox = tx.objectStore(STORE.outbox)
  for (const seq of seqs) outbox.delete(seq)
  await done(tx)
}

/**
 * Moves rejected operations into a separate store and drops them from the queue.
 *
 * Leaving them in the queue is not an option: it would jam forever and the user would lose
 * everything behind them. Discarding them silently is not an option either — so they land
 * here and raise a conspicuous red banner.
 */
export async function moveToDeadLetter(
  entries: { seq: number; op: Op; reason: string }[],
  at: number,
): Promise<void> {
  if (entries.length === 0) return
  const db = await openDB()
  const tx = db.transaction([STORE.outbox, STORE.deadletter], 'readwrite')
  const outbox = tx.objectStore(STORE.outbox)
  const dead = tx.objectStore(STORE.deadletter)
  for (const e of entries) {
    dead.put({ op_id: e.op.op_id, op: e.op, reason: e.reason, at })
    outbox.delete(e.seq)
  }
  await done(tx)
}

/**
 * Wipes local data on logout.
 *
 * The queue and the dead letters are deliberately NOT touched: an unsent workout may be
 * sitting there, and history is inviolable. Logging out is no reason to lose sets.
 */
export async function clearUserData(): Promise<void> {
  const db = await openDB()
  const tx = db.transaction([STORE.sessions, STORE.sets, STORE.meta], 'readwrite')
  tx.objectStore(STORE.sessions).clear()
  tx.objectStore(STORE.sets).clear()
  tx.objectStore(STORE.meta).delete('cursor')
  tx.objectStore(STORE.meta).delete('user')
  tx.objectStore(STORE.meta).delete('active_session')
  await done(tx)
}

/** Test-only: closes the connection so the next test opens its own. */
export function resetForTests(): void {
  if (dbPromise) {
    void dbPromise.then((db) => db.close()).catch(() => {})
    dbPromise = null
  }
}
