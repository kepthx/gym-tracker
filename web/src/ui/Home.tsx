import { deleteWorkout, startWorkout } from '../state/actions'
import { currentProgram, getState, navigate, programFor } from '../state/store'
import { doneCount, draft, history, lastDoneAt, setsOf } from '../state/selectors'
import { ConfirmInline } from './ConfirmInline'
import { fmtDate, fmtStarted, plural, workoutsRecorded } from './format'
import { SaveStatusBar, SaveStatusChip } from './SaveStatus'
import './home.css'

export function HomeScreen({ onLogout }: { onLogout: () => void }) {
  const state = getState()
  const program = currentProgram()
  const unfinished = draft(state.sessions)
  const recorded = history(state.sessions).length

  return (
    <>
      <header class="topbar">
        <div class="col topbar-row">
          <h1 class="topbar-title">Тренировки</h1>
          <div class="spacer" />
          <SaveStatusChip />
        </div>
        <div class="col status-line">
          {program ? `${program.name} · ${workoutsRecorded(recorded)}` : 'Программа не задана'}
        </div>
      </header>

      <SaveStatusBar onLogin={() => navigate({ name: 'login' })} />

      <div class="scroll">
        <div class="col home-body">
          {unfinished && <DraftCard sessionID={unfinished.id} />}

          {!program && (
            <div class="empty">
              Программа не задана. Создайте файл программы на сервере — карточки дней
              появятся после перезапуска.
            </div>
          )}

          {program?.days.map((day, i) => {
            const last = lastDoneAt(state.sessions, day.id)
            return (
              <button
                key={day.id}
                class="card-tap day"
                onClick={() => {
                  void startWorkout(day.id, state.currentProgramHash!).then((id) =>
                    navigate({ name: 'workout', sessionID: id }),
                  )
                }}
              >
                <div class="day-head">
                  <h2 class="card-title">
                    {i + 1}. {day.name}
                  </h2>
                  <span class="card-meta">{last ? fmtDate(last) : 'ещё не было'}</span>
                </div>
                <div class="card-sub">{day.muscles}</div>
                <div class="day-exercises">
                  {day.exercises.map((e) => e.name).join(' · ')}
                </div>
              </button>
            )
          })}

          <div class="section-gap" />

          <button class="btn btn-primary btn-wide" onClick={() => navigate({ name: 'progress' })}>
            Прогресс
          </button>

          <div class="section-gap" />

          <div class="home-footer">
            <a class="btn btn-quiet" href="/api/export" download>
              Выгрузить всё (JSON)
            </a>
            <button class="btn btn-quiet" onClick={() => navigate({ name: 'diagnostics' })}>
              Диагностика
            </button>
            <ConfirmInline
              label="Выйти из аккаунта"
              question="Выйти?"
              confirmLabel="Выйти"
              onConfirm={onLogout}
            />
          </div>
          <div class="section-gap" />
        </div>
      </div>
    </>
  )
}

/** The unfinished-workout card — conspicuous, and above the list of days. */
function DraftCard({ sessionID }: { sessionID: string }) {
  const state = getState()
  const session = state.sessions.find((s) => s.id === sessionID)!
  const program = programFor(session.program_hash)
  const day = program?.days.find((d) => d.id === session.day_id)
  const done = doneCount(setsOf(state.sets, sessionID))

  return (
    <section class="card draft">
      <div class="draft-label">Незавершённая тренировка</div>
      <h2 class="card-title">{day ? day.name : session.day_id}</h2>
      <div class="card-sub">
        {done} {plural(done, 'подход', 'подхода', 'подходов')} · начата{' '}
        {fmtStarted(session.started_at)}
      </div>
      <div class="draft-actions">
        <button
          class="btn btn-primary"
          onClick={() => navigate({ name: 'workout', sessionID })}
        >
          Продолжить
        </button>
        <ConfirmInline
          label="Удалить"
          question="Удалить тренировку?"
          confirmLabel="Удалить"
          danger
          onConfirm={() => void deleteWorkout(sessionID)}
        />
      </div>
    </section>
  )
}
