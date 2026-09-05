/** Types shared across the client. The shape matches what the server sends and accepts. */

export type OpType = 'session.start' | 'set.upsert' | 'session.finish' | 'session.delete'

export interface Op {
  op_id: string
  ts: number
  seq: number
  type: OpType
  session_id: string

  date?: string
  day_id?: string
  started_at?: number
  program_hash?: string

  exercise_id?: string
  idx?: number
  done?: boolean
  weight?: number | null
  reps?: string | null

  finished_at?: number
}

export type OpStatus = 'applied' | 'duplicate' | 'rejected'

export interface OpResult {
  op_id: string
  status: OpStatus
  reason?: string
  warning?: string
  closed_session_id?: string
}

export interface SessionRow {
  id: string
  date: string
  day_id: string
  program_hash: string
  started_at: number
  finished_at: number | null
  deleted: boolean
  /** Reserved: nothing writes it yet. Mirrors sessions.note in the schema. */
  note: string
  updated_ts: number
  updated_by: string
  rev: number
}

export interface SetRow {
  session_id: string
  exercise_id: string
  idx: number
  done: boolean
  weight: number | null
  reps: string | null
  /**
   * Reserved: nothing writes it yet, the selectors already honour it. When an op does, it
   * must be a monotone tombstone like a session's — never a plain LWW field.
   */
  deleted: boolean
  updated_ts: number
  updated_by: string
  rev: number
}

export interface ProgramRow {
  hash: string
  json: Program
}

export interface ChangeSet {
  sessions: SessionRow[]
  sets: SetRow[]
  programs: ProgramRow[]
}

export interface SyncResponse {
  cursor: number
  results: OpResult[]
  changes: ChangeSet
  has_more: boolean
  server_time: number
}

export interface Exercise {
  id: string
  name: string
  scheme: string
  sets: number
  default_reps: string
  weighted: boolean
  /** Progress runs downwards: the number is machine assistance, so less of it is better. */
  lower_is_better?: boolean
  groups?: string[]
  rest_sec?: number
}

export interface Day {
  id: string
  name: string
  muscles: string
  exercises: Exercise[]
}

export interface Program {
  version: number
  name: string
  days: Day[]
}

/**
 * The technique reference for one exercise.
 *
 * Guides are keyed by exercise_id rather than carried inside the program: a program is
 * canonicalised and hashed, and history renders from that snapshot, so prose in there would
 * mint a new snapshot on every comma. An exercise id is forever, which is what lets one
 * guide serve a workout recorded against a program that has since been replaced.
 */
/**
 * The demonstration for one exercise.
 *
 * `clip` is a short silent loop, `frames` the two end positions of the movement, crossfaded.
 * Both are served from this origin. There is no file name here: the files are derived from
 * the exercise id, which is what keeps the guides file from being able to name a path.
 *
 * `credit` and `license` are shown under the demonstration. The clips are CC BY, so that is
 * a licence condition, not a nicety.
 */
export interface ExerciseMedia {
  kind: 'clip' | 'frames'
  credit: string
  license: string
  source: string
}

export interface ExerciseGuide {
  summary: string
  cues: string[]
  mistakes?: string[]
  media?: ExerciseMedia
}

export interface User {
  id: number
  username: string
  display_name: string
  is_admin: boolean
}

/** An entry in the outbox queue. IndexedDB issues the key, and it also fixes the order. */
export interface OutboxEntry {
  seq?: number
  op: Op
}

export interface DeadLetter {
  op_id: string
  op: Op
  reason: string
  at: number
}
