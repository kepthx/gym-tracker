import { useEffect, useState } from 'preact/hooks'
import { deadLetters, dismissDeadLetters, getMeta, outboxHead } from '../db/idb'
import { engine } from '../sync/engine'
import { getState, navigate } from '../state/store'
import type { DeadLetter, Op } from '../types'
import { useSyncStatus } from './SaveStatus'
import './diagnostics.css'

/**
 * The diagnostics screen. It is not decoration: the "visible save status" requirement only
 * means something if you can look behind the indicator and see what exactly did not go out.
 */
export function DiagnosticsScreen() {
  const status = useSyncStatus()
  const state = getState()
  const [queue, setQueue] = useState<{ seq: number; op: Op }[]>([])
  const [dead, setDead] = useState<DeadLetter[]>([])
  const [cursor, setCursor] = useState<number>(0)
  const [persisted, setPersisted] = useState<boolean | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    void (async () => {
      setQueue(await outboxHead(50))
      setDead(await deadLetters())
      setCursor((await getMeta<number>('cursor')) ?? 0)
      setPersisted((await getMeta<boolean>('persisted')) ?? null)
    })()
  }, [status])

  /**
   * Acknowledging a rejection. The reason has been read; the entry goes, and with it the
   * red indicator — an indicator that is always red is one nobody reads. The data behind the
   * operation stays on the device: this drops the record of the refusal, not the set.
   */
  async function dismiss(opIDs: string[]) {
    await dismissDeadLetters(opIDs)
    setDead(await deadLetters())
    await engine.recount()
  }

  async function copyQueue() {
    const dump = JSON.stringify({ queue, dead, cursor, status }, null, 2)
    try {
      await navigator.clipboard.writeText(dump)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setCopied(false)
    }
  }

  return (
    <>
      <header class="topbar">
        <div class="col topbar-row">
          <button class="btn btn-icon" onClick={() => navigate({ name: 'home' })}>
            ← Назад
          </button>
          <div class="spacer" />
        </div>
        <div class="col">
          <h1 class="topbar-title">Диагностика</h1>
        </div>
      </header>

      <div class="scroll">
        <div class="col diag-body">
          <dl class="diag">
            <Row label="Состояние" value={status.state} />
            <Row label="Ждёт отправки" value={String(status.pending)} />
            <Row label="Отклонено сервером" value={String(status.dead)} />
            <Row label="Курсор" value={String(cursor)} />
            <Row
              label="Последняя синхронизация"
              value={status.lastSyncAt ? new Date(status.lastSyncAt).toLocaleString('ru') : '—'}
            />
            <Row
              label="Расхождение часов"
              value={`${Math.round(status.clockSkew / 1000)} с`}
              warn={Math.abs(status.clockSkew) > 5 * 60_000}
            />
            <Row
              label="Хранилище защищено от вытеснения"
              value={persisted === null ? 'неизвестно' : persisted ? 'да' : 'нет'}
              warn={persisted === false}
            />
            <Row label="Тренировок локально" value={String(state.sessions.length)} />
            <Row label="Подходов локально" value={String(state.sets.length)} />
          </dl>

          {persisted === false && (
            <p class="diag-note">
              Браузер не закрепил хранилище за приложением. Добавьте приложение на экран
              «Домой» — так система перестаёт считать его данные временными.
            </p>
          )}

          {dead.length > 0 && (
            <>
              <h2 class="progress-section">Отклонено сервером</h2>
              <p class="diag-note">
                Сервер не принял эти действия. Записанное осталось на устройстве; убрать запись
                об отказе можно, когда причина понятна.
              </p>
              <ul class="diag-list">
                {dead.map((d) => (
                  <li key={d.op_id} class="diag-dead">
                    <span>
                      <code>{d.op.type}</code> — {d.reason}
                    </span>
                    <button class="btn btn-quiet btn-small" onClick={() => void dismiss([d.op_id])}>
                      Убрать
                    </button>
                  </li>
                ))}
              </ul>
              {dead.length > 1 && (
                <div class="diag-actions">
                  <button
                    class="btn btn-quiet"
                    onClick={() => void dismiss(dead.map((d) => d.op_id))}
                  >
                    Убрать все
                  </button>
                </div>
              )}
            </>
          )}

          {queue.length > 0 && (
            <>
              <h2 class="progress-section">Очередь</h2>
              <ul class="diag-list">
                {queue.map((entry) => (
                  <li key={entry.seq}>
                    <code>{entry.op.type}</code> {new Date(entry.op.ts).toLocaleTimeString('ru')}
                  </li>
                ))}
              </ul>
            </>
          )}

          <div class="diag-actions">
            <button class="btn" onClick={() => engine.retryNow()}>
              Отправить сейчас
            </button>
            <button class="btn btn-quiet" onClick={() => void copyQueue()}>
              {copied ? 'Скопировано' : 'Скопировать как JSON'}
            </button>
          </div>
          <div class="section-gap" />
        </div>
      </div>
    </>
  )
}

function Row({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div class="diag-row">
      <dt>{label}</dt>
      <dd class={warn ? 'diag-warn' : ''}>{value}</dd>
    </div>
  )
}
