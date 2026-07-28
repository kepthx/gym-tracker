import { applyLocal, getMeta, setMeta } from '../db/idb'
import { engine } from '../sync/engine'
import type { Op, SessionRow, SetRow } from '../types'
import { getState, reloadFromStorage } from './store'

/**
 * User actions.
 *
 * Each of them writes the new state AND enqueues the operation in one transaction, after
 * which the screen rereads the data from storage. Data is saved from the moment of the tap,
 * not from the moment some button is pressed at the end — the app can be killed at any second.
 */

let deviceID = ''
let seq = 0

export async function initActions(): Promise<void> {
  deviceID = (await getMeta<string>('device_id')) ?? ''
  seq = (await getMeta<number>('seq')) ?? 0
}

function nextSeq(): number {
  seq += 1
  void setMeta('seq', seq)
  return seq
}

function baseOp(type: Op['type'], sessionID: string): Op {
  return {
    op_id: crypto.randomUUID(),
    ts: Date.now(),
    seq: nextSeq(),
    type,
    session_id: sessionID,
  }
}

/** The user's local date: history is organised by calendar day, while the server lives in UTC. */
function localDate(at: number): string {
  const d = new Date(at)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export async function startWorkout(dayID: string, programHash: string): Promise<string> {
  const now = Date.now()
  const sessionID = crypto.randomUUID()

  const op: Op = {
    ...baseOp('session.start', sessionID),
    date: localDate(now),
    day_id: dayID,
    started_at: now,
    program_hash: programHash,
  }

  const row: SessionRow = {
    id: sessionID,
    date: op.date!,
    day_id: dayID,
    program_hash: programHash,
    started_at: now,
    finished_at: null,
    deleted: false,
    note: '',
    updated_ts: now,
    updated_by: deviceID,
    rev: 0,
  }

  // Exactly the same rule as on the server: the one started later stays open and the
  // previous one is closed at its start. Done locally right away so the screen does not
  // show two unfinished workouts while waiting for the server's answer.
  const open = getState().sessions.filter((s) => !s.finished_at && !s.deleted)

  await applyLocal(op, ({ sessions }) => {
    for (const prev of open) sessions.put({ ...prev, finished_at: now })
    sessions.put(row)
  })

  await setMeta('active_session', sessionID)
  await reloadFromStorage()
  engine.schedule()
  return sessionID
}

interface SetPatch {
  done: boolean
  weight: number | null
  reps: string | null
}

/**
 * Writes the whole set rather than the single field that changed.
 *
 * At the moment of the tap the client holds the set's full state, so sending the whole row
 * is free, and a whole class of merge bugs disappears.
 */
export async function upsertSet(
  sessionID: string,
  exerciseID: string,
  idx: number,
  patch: SetPatch,
): Promise<void> {
  const now = Date.now()

  const op: Op = {
    ...baseOp('set.upsert', sessionID),
    exercise_id: exerciseID,
    idx,
    done: patch.done,
    weight: patch.weight,
    reps: patch.reps,
  }

  const row: SetRow = {
    session_id: sessionID,
    exercise_id: exerciseID,
    idx,
    done: patch.done,
    weight: patch.weight,
    reps: patch.reps,
    deleted: false,
    updated_ts: now,
    updated_by: deviceID,
    rev: 0,
  }

  await applyLocal(op, ({ sets }) => sets.put(row))
  await reloadFromStorage()
  engine.schedule()
}

export async function finishWorkout(sessionID: string): Promise<void> {
  const now = Date.now()
  const op: Op = { ...baseOp('session.finish', sessionID), finished_at: now }

  const current = getState().sessions.find((s) => s.id === sessionID)
  await applyLocal(op, ({ sessions }) => {
    if (current) sessions.put({ ...current, finished_at: now })
  })

  await setMeta('active_session', null)
  await reloadFromStorage()
  engine.schedule(0)
}

/**
 * Deleting a workout. Available only for an unfinished one and only through an in-UI
 * confirmation: recorded history must not be lost without explicit intent.
 */
export async function deleteWorkout(sessionID: string): Promise<void> {
  const op: Op = baseOp('session.delete', sessionID)

  const current = getState().sessions.find((s) => s.id === sessionID)
  await applyLocal(op, ({ sessions }) => {
    if (current) sessions.put({ ...current, deleted: true })
  })

  await setMeta('active_session', null)
  await reloadFromStorage()
  engine.schedule(0)
}
