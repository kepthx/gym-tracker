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
  /** Carries idx: a skipped set means position in this array is not the set number. */
  sets: { idx: number; weight: number | null; reps: string | null }[]
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
      return {
        at: session.started_at,
        sets: done.map((s) => ({ idx: s.idx, weight: s.weight, reps: s.reps })),
      }
    }
  }
  return null
}

/**
 * Last time's numbers for one particular set.
 *
 * Matched on idx rather than on position: a set skipped last time would otherwise shift every
 * later set's hint by one, and a hint that quietly names the wrong set is worse than none.
 */
export function lastSetAt(previous: LastResult | null, idx: number): LastResult['sets'][number] | null {
  return previous?.sets.find((s) => s.idx === idx) ?? null
}

/**
 * Whether one result beats another for this exercise.
 *
 * On an assisted machine the recorded number is the help the machine gives, so progress
 * runs downwards: 30 kg of assistance beats 35. Everything that decides "best" — the record
 * marker, the chart's highlighted point, the caption — goes through here, because without
 * it the app congratulates the user for going backwards.
 */
export function isBetter(candidate: number, current: number, lowerIsBetter = false): boolean {
  return lowerIsBetter ? candidate < current : candidate > current
}

/** The all-time best working weight, excluding the current workout. */
export function bestWeight(
  sessions: SessionRow[],
  sets: SetRow[],
  exerciseID: string,
  exceptSessionID: string,
  lowerIsBetter = false,
): number | null {
  const alive = new Set(sessions.filter((s) => !s.deleted && s.id !== exceptSessionID).map((s) => s.id))

  let best: number | null = null
  for (const set of sets) {
    if (set.exercise_id !== exerciseID || !set.done || set.deleted) continue
    if (!alive.has(set.session_id)) continue
    if (set.weight === null) continue
    if (best === null || isBetter(set.weight, best, lowerIsBetter)) best = set.weight
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
  lowerIsBetter = false,
): ProgressPoint[] {
  const bySession = new Map<string, number>()

  for (const set of sets) {
    if (set.exercise_id !== exerciseID || !set.done || set.deleted || set.weight === null) continue
    const best = bySession.get(set.session_id)
    if (best === undefined || isBetter(set.weight, best, lowerIsBetter)) {
      bySession.set(set.session_id, set.weight)
    }
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
export interface Chartable {
  id: string
  name: string
  points: ProgressPoint[]
  lowerIsBetter: boolean
}

export function chartableExercises(
  sessions: SessionRow[],
  sets: SetRow[],
  programs: Map<string, Program>,
): Chartable[] {
  // The direction travels with the exercise rather than being looked up again downstream:
  // an id may appear in several program snapshots, and the chart must not flip direction
  // depending on which one happened to be read last.
  const known = new Map<string, { name: string; lowerIsBetter: boolean }>()
  for (const program of programs.values()) {
    for (const day of program.days) {
      for (const exercise of day.exercises) {
        if (!exercise.weighted) continue
        known.set(exercise.id, {
          name: exercise.name,
          lowerIsBetter: exercise.lower_is_better === true,
        })
      }
    }
  }

  const out: Chartable[] = []
  for (const [id, { name, lowerIsBetter }] of known) {
    const points = progressSeries(sessions, sets, id, lowerIsBetter)
    if (points.length >= 2) out.push({ id, name, points, lowerIsBetter })
  }
  return out.sort((a, b) => b.points.length - a.points.length)
}
