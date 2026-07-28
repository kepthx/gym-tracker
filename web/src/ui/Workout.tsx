import { useEffect, useRef, useState } from 'preact/hooks'
import { finishWorkout, upsertSet } from '../state/actions'
import { getState, navigate, programFor } from '../state/store'
import {
  bestWeight,
  doneCount,
  lastResult,
  setAt,
  setsOf,
  totalSets,
} from '../state/selectors'
import type { Exercise, SetRow } from '../types'
import { ConfirmInline } from './ConfirmInline'
import { fmtDate, fmtSets, fmtWeight, isWeightInputValid, parseWeight } from './format'
import { SaveStatusBar, SaveStatusChip } from './SaveStatus'
import './workout.css'

export function WorkoutScreen({ sessionID }: { sessionID: string }) {
  const state = getState()
  const session = state.sessions.find((s) => s.id === sessionID)

  if (!session) {
    return (
      <div class="scroll">
        <div class="col empty">Тренировка не найдена.</div>
      </div>
    )
  }

  // A workout is rendered with the program in force when it started, not the current one:
  // otherwise changing the program would retroactively rewrite what was already recorded.
  const program = programFor(session.program_hash)
  const day = program?.days.find((d) => d.id === session.day_id)
  const sets = setsOf(state.sets, sessionID)

  const done = doneCount(sets)
  const total = totalSets(program, session.day_id)
  const percent = total > 0 ? Math.round((done / total) * 100) : 0

  return (
    <>
      <header class="topbar">
        <div class="col topbar-row">
          <ConfirmInline
            label="← Выйти"
            question="Выйти без завершения?"
            confirmLabel="Выйти"
            onConfirm={() => navigate({ name: 'home' })}
          />
          <div class="spacer" />
          <span class="workout-counter">
            {done}/{total}
          </span>
          <SaveStatusChip />
        </div>
        <div class="col">
          <div class="workout-day">{day ? day.name : session.day_id}</div>
          <div class="progress" role="progressbar" aria-valuenow={percent}>
            <div class="progress-fill" style={{ width: `${percent}%` }} />
          </div>
        </div>
      </header>

      <SaveStatusBar onLogin={() => navigate({ name: 'login' })} />

      <div class="scroll">
        <div class="col workout-body">
          {!day && (
            <div class="empty">
              Программа этой тренировки не загружена. Данные на месте, они появятся после
              синхронизации.
            </div>
          )}

          {day?.exercises.map((exercise) => (
            <ExerciseCard
              key={exercise.id}
              exercise={exercise}
              sessionID={sessionID}
              sets={sets}
            />
          ))}

          <button
            class="btn btn-primary btn-wide finish"
            disabled={done === 0}
            onClick={() => {
              void finishWorkout(sessionID).then(() => navigate({ name: 'home' }))
            }}
          >
            Завершить тренировку
          </button>
          <div class="section-gap" />
        </div>
      </div>
    </>
  )
}

function ExerciseCard({
  exercise,
  sessionID,
  sets,
}: {
  exercise: Exercise
  sessionID: string
  sets: SetRow[]
}) {
  const state = getState()
  const [editing, setEditing] = useState<number | null>(null)

  const mine = sets.filter((s) => s.exercise_id === exercise.id)
  const done = doneCount(mine)

  const previous = lastResult(state.sessions, state.sets, exercise.id, sessionID)
  const best = bestWeight(state.sessions, state.sets, exercise.id, sessionID)
  const todayBest = mine.reduce<number | null>(
    (top, s) => (s.done && s.weight !== null && (top === null || s.weight > top) ? s.weight : top),
    null,
  )
  const isRecord = exercise.weighted && todayBest !== null && (best === null || todayBest > best)

  return (
    <section class="card exercise">
      <div class="exercise-head">
        <div>
          <h2 class="card-title">
            {exercise.name}
            {isRecord && <span class="record"> рекорд</span>}
          </h2>
          <div class="card-sub">{exercise.scheme}</div>
        </div>
        <span class="exercise-count">
          {done}/{exercise.sets}
        </span>
      </div>

      {previous ? (
        <div class="last-result">
          Прошлый раз ({fmtDate(previous.at)}): {fmtSets(previous.sets)}
        </div>
      ) : (
        <div class="last-result muted">Прошлый раз: ещё не было</div>
      )}

      <div class="set-row">
        {Array.from({ length: exercise.sets }, (_, idx) => (
          <SetColumn
            key={idx}
            idx={idx}
            exercise={exercise}
            sessionID={sessionID}
            row={setAt(mine, exercise.id, idx)}
            editing={editing === idx}
            onEdit={setEditing}
          />
        ))}
      </div>
    </section>
  )
}

function SetColumn({
  idx,
  exercise,
  sessionID,
  row,
  editing,
  onEdit,
}: {
  idx: number
  exercise: Exercise
  sessionID: string
  row: SetRow | undefined
  editing: boolean
  onEdit: (idx: number | null) => void
}) {
  const done = row?.done ?? false

  /**
   * Tapping the button IMMEDIATELY writes the mark with the default reps and highlights
   * the button at once. The reps editor opens only after that. So the data is saved from
   * the moment of the tap, not from the moment of confirmation: the default is right in
   * most cases, and editing stays an exception rather than a mandatory step.
   */
  function toggle() {
    if (done) {
      void upsertSet(sessionID, exercise.id, idx, {
        done: false,
        weight: row?.weight ?? null,
        reps: row?.reps ?? null,
      })
      onEdit(null)
      return
    }
    void upsertSet(sessionID, exercise.id, idx, {
      done: true,
      weight: row?.weight ?? null,
      reps: exercise.default_reps,
    })
    onEdit(idx)
  }

  return (
    <div class="set-col">
      <button
        class={`set-btn ${done ? 'set-btn-done' : ''}`}
        onClick={toggle}
        aria-label={`Подход ${idx + 1}`}
      >
        {done ? (row?.reps ?? exercise.default_reps) : '—'}
      </button>

      {exercise.weighted && (
        <WeightField
          value={row?.weight ?? null}
          onCommit={(weight) => {
            void upsertSet(sessionID, exercise.id, idx, {
              done: row?.done ?? false,
              weight,
              reps: row?.reps ?? null,
            })
          }}
        />
      )}

      {editing && done && (
        <RepsEditor
          initial={row?.reps ?? exercise.default_reps}
          onDone={(reps) => {
            onEdit(null)
            if (reps !== (row?.reps ?? '')) {
              void upsertSet(sessionID, exercise.id, idx, {
                done: true,
                weight: row?.weight ?? null,
                reps,
              })
            }
          }}
        />
      )}
    </div>
  )
}

function WeightField({
  value,
  onCommit,
}: {
  value: number | null
  onCommit: (weight: number | null) => void
}) {
  const [text, setText] = useState(fmtWeight(value))
  const touched = useRef(false)

  // Until the field is touched it follows the data: it may have arrived from another device.
  useEffect(() => {
    if (!touched.current) setText(fmtWeight(value))
  }, [value])

  function commit() {
    touched.current = false
    const parsed = parseWeight(text)
    setText(fmtWeight(parsed))
    if (parsed !== value) onCommit(parsed)
  }

  return (
    <input
      /* Never type="number": on a Russian layout the numeric keyboard produces a comma,
         type="number" discards it, and the field silently goes empty. inputmode="decimal"
         gives the same keyboard, and parseWeight handles the comma. */
      type="text"
      inputMode="decimal"
      enterKeyHint="done"
      autocomplete="off"
      autocorrect="off"
      autocapitalize="off"
      spellcheck={false}
      class="weight-field"
      placeholder="кг"
      value={text}
      onInput={(e) => {
        const next = (e.currentTarget as HTMLInputElement).value
        touched.current = true
        if (isWeightInputValid(next)) setText(next)
        else (e.currentTarget as HTMLInputElement).value = text
      }}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === 'Enter') (e.currentTarget as HTMLInputElement).blur()
      }}
    />
  )
}

function RepsEditor({
  initial,
  onDone,
}: {
  initial: string
  onDone: (reps: string) => void
}) {
  const [text, setText] = useState(initial)
  const ref = useRef<HTMLInputElement>(null)

  useEffect(() => {
    ref.current?.focus()
    ref.current?.select()
  }, [])

  return (
    <div class="reps-editor">
      <input
        ref={ref}
        /* Reps are text: they can be «12», «8/нога», «30с», «40м». */
        type="text"
        inputMode="text"
        enterKeyHint="done"
        autocomplete="off"
        class="reps-field"
        value={text}
        onInput={(e) => setText((e.currentTarget as HTMLInputElement).value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') onDone(text.trim())
        }}
        onBlur={() => onDone(text.trim())}
      />
    </div>
  )
}
