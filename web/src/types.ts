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
