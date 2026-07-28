import type { Program, SessionRow, SetRow } from '../types'

/**
 * Everything derived is computed on the client from data in local storage.
 *
 * After the first full sync the entire history sits in IndexedDB (about five thousand rows
 * a year, some three hundred kilobytes), so "last time", the record marker and the charts
 * all work offline and need not a single request to the server.
 */

export interface Workout {
  session: SessionRow
  sets: SetRow[]
}

/** Workouts in history: finished and not deleted, most recent first. */
export function history(sessions: SessionRow[]): SessionRow[] {
  return sessions
    .filter((s) => !s.deleted && s.finished_at !== null)
    .sort((a, b) => b.started_at - a.started_at)
}

/** The unfinished workout. There can be only one. */
export function draft(sessions: SessionRow[]): SessionRow | null {
  const open = sessions
    .filter((s) => !s.deleted && s.finished_at === null)
    .sort((a, b) => b.started_at - a.started_at)
  return open[0] ?? null
}

export function setsOf(sets: SetRow[], sessionID: string): SetRow[] {
  return sets.filter((s) => s.session_id === sessionID && !s.deleted)
}

export function setAt(sets: SetRow[], exerciseID: string, idx: number): SetRow | undefined {
  return sets.find((s) => s.exercise_id === exerciseID && s.idx === idx && !s.deleted)
}

export function doneCount(sets: SetRow[]): number {
  return sets.filter((s) => s.done).length
}

/** The counter's denominator comes from the snapshot of THE program the workout ran under. */
export function totalSets(program: Program | null, dayID: string): number {
  const day = program?.days.find((d) => d.id === dayID)
  if (!day) return 0
  return day.exercises.reduce((sum, e) => sum + e.sets, 0)
}

export interface LastResult {
  at: number
  sets: { weight: number | null; reps: string | null }[]
}

/**
 * Last time's result for this exercise — the key element of the workout screen: it is what
 * the user decides today's weight from.
 */
export function lastResult(
  sessions: SessionRow[],
  sets: SetRow[],
  exerciseID: string,
  exceptSessionID: string,
): LastResult | null {
  const candidates = sessions
    .filter((s) => !s.deleted && s.id !== exceptSessionID)
    .sort((a, b) => b.started_at - a.started_at)

  for (const session of candidates) {
    const done = sets
      .filter((s) => s.session_id === session.id && s.exercise_id === exerciseID && s.done && !s.deleted)
      .sort((a, b) => a.idx - b.idx)
    if (done.length > 0) {
      return { at: session.started_at, sets: done.map((s) => ({ weight: s.weight, reps: s.reps })) }
    }
  }
  return null
}

/** The all-time best working weight, excluding the current workout. */
export function bestWeight(
  sessions: SessionRow[],
  sets: SetRow[],
  exerciseID: string,
  exceptSessionID: string,
): number | null {
  const alive = new Set(sessions.filter((s) => !s.deleted && s.id !== exceptSessionID).map((s) => s.id))

  let best: number | null = null
  for (const set of sets) {
    if (set.exercise_id !== exerciseID || !set.done || set.deleted) continue
    if (!alive.has(set.session_id)) continue
    if (set.weight === null) continue
    if (best === null || set.weight > best) best = set.weight
  }
  return best
}

export interface ProgressPoint {
  at: number
  weight: number
  sessionID: string
}

/** The series of best working weight by date for the progress chart. */
export function progressSeries(
  sessions: SessionRow[],
  sets: SetRow[],
  exerciseID: string,
): ProgressPoint[] {
  const bySession = new Map<string, number>()

  for (const set of sets) {
    if (set.exercise_id !== exerciseID || !set.done || set.deleted || set.weight === null) continue
    const best = bySession.get(set.session_id)
    if (best === undefined || set.weight > best) bySession.set(set.session_id, set.weight)
  }

  const points: ProgressPoint[] = []
  for (const session of sessions) {
    if (session.deleted || session.finished_at === null) continue
    const weight = bySession.get(session.id)
    if (weight === undefined) continue
    points.push({ at: session.started_at, weight, sessionID: session.id })
  }
  return points.sort((a, b) => a.at - b.at)
}

/** When this program day was last performed. */
export function lastDoneAt(sessions: SessionRow[], dayID: string): number | null {
  let latest: number | null = null
  for (const session of sessions) {
    if (session.deleted || session.day_id !== dayID || session.finished_at === null) continue
    if (latest === null || session.started_at > latest) latest = session.started_at
  }
  return latest
}

/** Weighted exercises with at least two workouts — only those get a chart. */
export function chartableExercises(
  sessions: SessionRow[],
  sets: SetRow[],
  programs: Map<string, Program>,
): { id: string; name: string; points: ProgressPoint[] }[] {
  const names = new Map<string, string>()
  for (const program of programs.values()) {
    for (const day of program.days) {
      for (const exercise of day.exercises) {
        if (exercise.weighted) names.set(exercise.id, exercise.name)
      }
    }
  }

  const out: { id: string; name: string; points: ProgressPoint[] }[] = []
  for (const [id, name] of names) {
    const points = progressSeries(sessions, sets, id)
    if (points.length >= 2) out.push({ id, name, points })
  }
  return out.sort((a, b) => b.points.length - a.points.length)
}
