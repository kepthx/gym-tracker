import { getState, navigate, programFor } from '../state/store'
import { chartableExercises, doneCount, history, setsOf } from '../state/selectors'
import { Sparkline } from './Chart'
import { fmtDate, fmtWeight, plural } from './format'
import { SaveStatusBar } from './SaveStatus'
import './progress.css'

export function ProgressScreen() {
  const state = getState()
  const charts = chartableExercises(state.sessions, state.sets, state.programs)
  const recent = history(state.sessions).slice(0, 20)

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
          <h1 class="topbar-title">Прогресс</h1>
        </div>
      </header>

      <SaveStatusBar onLogin={() => navigate({ name: 'login' })} />

      <div class="scroll">
        <div class="col progress-body">
          {charts.length === 0 ? (
            <div class="empty">
              Графики появятся, когда по упражнению с весом наберётся хотя бы две
              завершённые тренировки. Сейчас записано {recent.length}{' '}
              {plural(recent.length, 'тренировка', 'тренировки', 'тренировок')}.
            </div>
          ) : (
            charts.map((chart) => {
              const max = Math.max(...chart.points.map((p) => p.weight))
              return (
                <section class="card" key={chart.id}>
                  <div class="progress-head">
                    <h2 class="card-title">{chart.name}</h2>
                    <span class="progress-max">{fmtWeight(max)} кг</span>
                  </div>
                  <Sparkline points={chart.points} />
                </section>
              )
            })
          )}

          <div class="section-gap" />
          <h2 class="progress-section">Последние тренировки</h2>

          {recent.length === 0 ? (
            <div class="empty">Завершённых тренировок пока нет.</div>
          ) : (
            <ul class="recent">
              {recent.map((session) => {
                const program = programFor(session.program_hash)
                const day = program?.days.find((d) => d.id === session.day_id)
                const done = doneCount(setsOf(state.sets, session.id))
                return (
                  <li class="recent-row" key={session.id}>
                    <span class="recent-day">{day ? day.name : session.day_id}</span>
                    <span class="recent-date">{fmtDate(session.started_at)}</span>
                    <span class="recent-sets">
                      {done} {plural(done, 'подход', 'подхода', 'подходов')}
                    </span>
                  </li>
                )
              })}
            </ul>
          )}
          <div class="section-gap" />
        </div>
      </div>
    </>
  )
}
