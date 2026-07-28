import { useEffect, useState } from 'preact/hooks'
import { engine, type SyncStatus } from '../sync/engine'
import { navigate } from '../state/store'
import './save-status.css'

/**
 * The autosave indicator.
 *
 * Two principles, without which it is useless:
 *
 * 1. Show a number. "Saved on device · 7" earns trust; a crossed-out cloud does not.
 * 2. Amber is not red. A queue with no signal at the gym is an expected state, not a
 *    failure. If it looks alarming, people stop believing the indicator, and the "visible
 *    save status" requirement dies along with that trust.
 */

export function useSyncStatus(): SyncStatus {
  const [status, setStatus] = useState<SyncStatus>(engine.getStatus())
  useEffect(() => engine.subscribe(setStatus), [])
  return status
}

/** The compact indicator in the workout screen's header. */
export function SaveStatusChip() {
  const status = useSyncStatus()

  switch (status.state) {
    case 'syncing':
      return <span class="chip chip-muted">Синхронизация…</span>
    case 'local':
      return <span class="chip chip-local">Сохранено на устройстве · {status.pending}</span>
    case 'error':
      return <span class="chip chip-error">Не сохранено · {status.pending + status.dead}</span>
    case 'auth':
      return <span class="chip chip-error">Требуется вход</span>
    case 'degraded':
      return <span class="chip chip-error">Хранилище недоступно</span>
    default:
      return <span class="chip chip-muted">Сохранено</span>
  }
}

/**
 * A full-width banner for states that need attention. It cannot be dismissed by tapping:
 * silent data loss is unacceptable, and so, therefore, is the silent disappearance of the
 * warning about it.
 */
export function SaveStatusBar({ onLogin }: { onLogin?: () => void }) {
  const status = useSyncStatus()

  if (status.state === 'auth') {
    return (
      <div class="alert alert-error">
        <span>Требуется вход. Записанное сохранено на устройстве и уйдёт после входа.</span>
        <button class="btn btn-quiet alert-action" onClick={onLogin}>
          Войти
        </button>
      </div>
    )
  }

  if (status.state === 'degraded') {
    return (
      <div class="alert alert-error">
        <span>Хранилище недоступно. Не закрывайте приложение, пока не появится связь.</span>
      </div>
    )
  }

  if (status.state === 'error') {
    const stuck = status.pending + status.dead
    return (
      <div class="alert alert-error">
        <span>Не сохранено на сервере · {stuck}</span>
        <button class="btn btn-quiet alert-action" onClick={() => engine.retryNow()}>
          Повторить
        </button>
        {status.dead > 0 && (
          <button
            class="btn btn-quiet alert-action"
            onClick={() => navigate({ name: 'diagnostics' })}
          >
            Подробнее
          </button>
        )}
      </div>
    )
  }

  return null
}
