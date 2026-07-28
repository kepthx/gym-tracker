import { useState } from 'preact/hooks'
import { ApiError, login, OfflineError } from '../sync/client'
import './login.css'

export function LoginScreen({ onSuccess }: { onSuccess: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: Event) {
    e.preventDefault()
    if (busy || password === '') return

    setBusy(true)
    setError('')
    try {
      await login(password)
      setPassword('')
      onSuccess()
    } catch (err) {
      if (err instanceof OfflineError) {
        setError('Нет связи. Записанное сохранено на устройстве.')
      } else if (err instanceof ApiError && err.status === 429) {
        const minutes = Math.ceil(err.retryAfter / 60)
        setError(`Слишком много попыток. Повторите через ${minutes} мин.`)
      } else if (err instanceof ApiError && err.status === 401) {
        setError('Неверный пароль.')
      } else {
        setError('Войти не удалось.')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div class="scroll">
      <form class="col login" onSubmit={submit}>
        <h1 class="topbar-title login-title">Тренировки</h1>

        <input
          type="password"
          class="field"
          placeholder="Пароль"
          autocomplete="current-password"
          enterKeyHint="go"
          value={password}
          onInput={(e) => setPassword((e.currentTarget as HTMLInputElement).value)}
        />

        <button class="btn btn-primary btn-wide" type="submit" disabled={busy || password === ''}>
          {busy ? 'Проверяю…' : 'Войти'}
        </button>

        <div class="field-error">{error}</div>

        {/* A home-screen app's storage is separate from Safari's, so the login is asked
            for once more inside the installed app. Without this line, an evening goes
            into debugging a non-bug. */}
        <p class="login-note">
          Приложение, добавленное на экран «Домой», хранит вход отдельно от Safari — там
          пароль спросят один раз заново. Дальше он не понадобится месяцами.
        </p>
      </form>
    </div>
  )
}
