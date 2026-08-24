import { allPrograms, allSessions, allSets, getMeta, setMeta, setMetaMany } from '../db/idb'
import type { ExerciseGuide, Program, SessionRow, SetRow, User } from '../types'

export type Screen =
  | { name: 'login' }
  | { name: 'home' }
  | { name: 'workout'; sessionID: string }
  | { name: 'progress' }
  | { name: 'diagnostics' }

export interface AppState {
  ready: boolean
  /** Storage is unavailable — run degraded and say so. */
  storageBroken: boolean
  user: User | null
  screen: Screen
  programs: Map<string, Program>
  currentProgramHash: string | null
  sessions: SessionRow[]
  sets: SetRow[]
  /** Technique reference, keyed by exercise_id. Empty until the first successful fetch. */
  guides: Record<string, ExerciseGuide>
}

const state: AppState = {
  ready: false,
  storageBroken: false,
  user: null,
  screen: { name: 'login' },
  programs: new Map(),
  currentProgramHash: null,
  sessions: [],
  sets: [],
  guides: {},
}

type Listener = () => void
const listeners = new Set<Listener>()

export function subscribe(listener: Listener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getState(): AppState {
  return state
}

function notify(): void {
  for (const listener of listeners) listener()
}

export function patchState(next: Partial<AppState>): void {
  Object.assign(state, next)
  notify()
}

/**
 * Rereads everything from local storage.
 *
 * A year accumulates on the order of five thousand rows — a fraction of a second — so a
 * full reread is simpler and more reliable than targeted updates, and leaves the screen
 * nowhere to drift from storage.
 */
export async function reloadFromStorage(): Promise<void> {
  const [sessions, sets, programs] = await Promise.all([allSessions(), allSets(), allPrograms()])
  state.sessions = sessions
  state.sets = sets
  state.programs = new Map(programs.map((p) => [p.hash, p.json]))
  notify()
}

export async function setCurrentProgramHash(hash: string): Promise<void> {
  state.currentProgramHash = hash
  await setMeta('current_program', hash)
  notify()
}

export async function loadCurrentProgramHash(): Promise<void> {
  state.currentProgramHash = (await getMeta<string>('current_program')) ?? null
}

/** The program in force at the time of this workout, not the current one. */
export function programFor(hash: string): Program | null {
  return state.programs.get(hash) ?? null
}

/**
 * The technique reference for an exercise, or null when there is none.
 *
 * Keyed by exercise_id, which never changes and is never reused, so this answer is right
 * for a workout recorded against an older program snapshot too.
 */
export function guideFor(exerciseID: string): ExerciseGuide | null {
  // An own-property check, not a bare lookup: an exercise id is only constrained to
  // [a-z0-9_], and "constructor" passes that. A bare read would return an inherited Object
  // member, which is truthy, and the card would render it straight into a TypeError.
  return Object.hasOwn(state.guides, exerciseID) ? (state.guides[exerciseID] ?? null) : null
}

export async function setGuides(guides: Record<string, ExerciseGuide>, etag: string): Promise<void> {
  // Storage first, then the screen, and both keys in one transaction. Rendering a response
  // that was never persisted is the failure mode the whole app is built to avoid, and it is
  // exactly what a rejected write (quota, degraded storage) would otherwise produce here.
  await setMetaMany({ guides, guides_etag: etag })
  state.guides = guides
  notify()
}

/**
 * Reads the reference saved last time.
 *
 * It has to happen before any network call: iOS restarts a home-screen app on nearly every
 * return, and the guide is exactly what gets opened at the gym where there is no signal.
 */
export async function loadGuides(): Promise<void> {
  state.guides = (await getMeta<Record<string, ExerciseGuide>>('guides')) ?? {}
}

export function currentProgram(): Program | null {
  if (!state.currentProgramHash) return null
  return state.programs.get(state.currentProgramHash) ?? null
}

/**
 * Navigation between screens.
 *
 * The current screen is saved to storage: iOS restarts a home-screen app on every return
 * and may open it at the start URL, so restoring the user's place is up to us.
 */
export function navigate(screen: Screen, push = true): void {
  state.screen = screen
  void setMeta('screen', screen)
  if (push && typeof history !== 'undefined') {
    history.pushState({ screen }, '')
  }
  notify()
}

export async function restoreScreen(): Promise<Screen | null> {
  return (await getMeta<Screen>('screen')) ?? null
}
