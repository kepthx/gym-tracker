import { useEffect, useState } from 'preact/hooks'
import { clearUserData, getMeta, setMeta } from './db/idb'
import { initActions } from './state/actions'
import {
  getState,
  loadCurrentProgramHash,
  loadGuides,
  navigate,
  patchState,
  reloadFromStorage,
  restoreScreen,
  setCurrentProgramHash,
  setGuides,
  subscribe,
} from './state/store'
import { draft } from './state/selectors'
import {
  ApiError,
  getGuides,
  getProgramHash,
  logout,
  me,
  OfflineError,
  setBearerToken,
} from './sync/client'
import { engine } from './sync/engine'
import { DiagnosticsScreen } from './ui/Diagnostics'
import { HomeScreen } from './ui/Home'
import { LoginScreen } from './ui/Login'
import { ProgressScreen } from './ui/Progress'
import { WorkoutScreen } from './ui/Workout'
import './styles/app.css'

export function App() {
  const [, setTick] = useState(0)
  useEffect(() => subscribe(() => setTick((t) => t + 1)), [])
  useEffect(() => void boot(), [])

  // There is no back button in standalone mode — only the edge swipe is left. Real history
  // entries make it safe: the swipe goes to the app's previous screen rather than throwing
  // the user out of it.
  useEffect(() => {
    const onPop = (e: PopStateEvent) => {
      const screen = (e.state as { screen?: Parameters<typeof navigate>[0] } | null)?.screen
      navigate(screen ?? { name: 'home' }, false)
    }
    addEventListener('popstate', onPop)
    return () => removeEventListener('popstate', onPop)
  }, [])

  const state = getState()

  if (!state.ready) return <div class="empty">Загрузка…</div>

  switch (state.screen.name) {
    case 'login':
      return <LoginScreen onSuccess={() => void afterLogin()} />
    case 'workout':
      return <WorkoutScreen sessionID={state.screen.sessionID} />
    case 'progress':
      return <ProgressScreen />
    case 'diagnostics':
      return <DiagnosticsScreen />
    default:
      return <HomeScreen onLogout={() => void signOut()} />
  }
}

/**
 * App startup.
 *
 * The screen renders from local storage BEFORE any network call: iOS restarts a home-screen
 * app on very nearly every return, and if rendering waited for a server response the app
 * would be useless in a basement gym.
 */
async function boot(): Promise<void> {
  try {
    await requestPersistentStorage()
    await initActions()
    await loadCurrentProgramHash()
    await loadGuides()
    await reloadFromStorage()
  } catch (err) {
    // Storage did not open. This turns into visible degradation rather than silent data
    // loss: the app works, but warns the user not to close it.
    console.error('хранилище недоступно', err)
    patchState({ ready: true, storageBroken: true, screen: { name: 'home' } })
    engine.markDegraded()
    return
  }

  const saved = await restoreScreen()
  const token = (await getMeta<string>('token')) ?? null
  setBearerToken(token)

  patchState({ ready: true, screen: saved ?? { name: 'home' } })

  engine.setOnChanged(() => {
    void reloadFromStorage()
    void refreshConfig()
  })
  // Connectivity returning is a trigger in its own right. The engine listens for it too, but
  // only to drain the outbox, and a sync that finds nothing to report never reaches
  // setOnChanged — which would leave a device that first launched offline with no guides
  // until it is killed and cold-started.
  addEventListener('online', () => void refreshConfig())
  await engine.init()

  try {
    const session = await me()
    patchState({ user: session.user })
    await setMeta('user', session.user)
    await refreshConfig()
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      navigate({ name: 'login' }, false)
      return
    }
    // No connection — run on local data. This is a normal scenario, not an error.
    if (!(err instanceof OfflineError)) console.error('не удалось проверить вход', err)
    const cached = await getMeta<{ id: number }>('user')
    if (!cached) navigate({ name: 'login' }, false)
  }
}

/**
 * WebKit grants storage persistence automatically to home-screen apps, with no dialog at
 * all. That materially lowers the risk of IndexedDB being evicted, so it is requested on
 * every startup and the result is visible in the diagnostics.
 */
async function requestPersistentStorage(): Promise<void> {
  if (!navigator.storage?.persisted) return
  try {
    const granted = (await navigator.storage.persisted()) || (await navigator.storage.persist())
    await setMeta('persisted', granted)
  } catch {
    // Unsupported is no reason to fail.
  }
}

/**
 * The two values that come from configuration rather than from the sync delta: which program
 * is current, and the technique reference.
 *
 * They refresh together whenever the app has reason to believe it can reach the server.
 * Guides matter most here: unlike program snapshots they have no second delivery path over
 * /api/sync, so a launch that happened offline has to pick them up when the signal returns
 * rather than wait for the next cold start that happens to have one.
 */
async function refreshConfig(): Promise<void> {
  await Promise.all([refreshProgramHash(), refreshGuides()])
}

/** The current program comes separately: the sync delta carries no marker of which one is active. */
async function refreshProgramHash(): Promise<void> {
  try {
    const body = await getProgramHash()
    await setCurrentProgramHash(body.hash)
  } catch (err) {
    // Offline, or no program set yet: the hash saved last time is what remains.
    if (!(err instanceof OfflineError) && !(err instanceof ApiError && err.status === 404)) {
      console.error('не удалось узнать текущую программу', err)
    }
  }
}

/**
 * The technique reference.
 *
 * A conditional request: the set is immutable until an admin reloads it, so the usual answer
 * is 304 and costs nothing. The copy in IndexedDB is what the guide renders from, which is
 * why an unreachable server here is silence rather than an error — offline is the normal
 * state at the gym, and the reference has to open there.
 */
async function refreshGuides(): Promise<void> {
  try {
    const etag = (await getMeta<string>('guides_etag')) ?? ''
    const body = await getGuides(etag)
    if (!body) return // 304 — what is already on the device is current.
    await setGuides(body.guides.exercises ?? {}, `"${body.hash}"`)
  } catch (err) {
    // Offline is the normal state at the gym and the reference still opens from IndexedDB,
    // so that stays silent. Anything else belongs in the console: a reference that never
    // arrives is otherwise indistinguishable from one that was never written.
    if (!(err instanceof OfflineError)) console.error('не удалось обновить справочник', err)
  }
}

async function afterLogin(): Promise<void> {
  const session = await me()
  patchState({ user: session.user })
  await setMeta('user', session.user)
  // Both start now; only the one the next screen needs is awaited. The guides are ~24 KB
  // that neither the home nor the workout screen reads, and awaiting them held the user on
  // the login form for the length of a cold fetch on gym signal. refreshGuides never rejects.
  const guides = refreshGuides()
  await refreshProgramHash()
  void guides
  engine.retryNow()

  // If a workout was left unfinished, go straight back into it.
  const unfinished = draft(getState().sessions)
  navigate(unfinished ? { name: 'workout', sessionID: unfinished.id } : { name: 'home' })
}

async function signOut(): Promise<void> {
  try {
    await logout()
  } catch {
    // Even if the server is unreachable, the local logout has to happen.
  }
  setBearerToken(null)
  await setMeta('token', null)
  // The outbox queue is deliberately left alone: a workout may be sitting in it, and
  // losing that is unacceptable.
  await clearUserData()
  patchState({ user: null, sessions: [], sets: [] })
  navigate({ name: 'login' })
}
