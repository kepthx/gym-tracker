import type { RefObject } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'
import { finishWorkout, setWeight, upsertSet } from '../state/actions'
import { getState, navigate, programFor } from '../state/store'
import {
  bestWeight,
  doneCount,
  isBetter,
  lastResult,
  lastSetAt,
  setAt,
  setsOf,
  totalSets,
} from '../state/selectors'
import type { LastResult } from '../state/selectors'
import type { Exercise, SetRow } from '../types'
import { ConfirmInline } from './ConfirmInline'
import { ExerciseGuide } from './ExerciseGuide'
import {
  fmtDate,
  fmtSets,
  fmtWeight,
  isWeightInputValid,
  joinReps,
  parseWeight,
  repsCount,
  repsUnit,
} from './format'
import { SaveStatusBar, SaveStatusChip } from './SaveStatus'
import './workout.css'

export function WorkoutScreen({ sessionID }: { sessionID: string }) {
  const state = getState()
  const [confirmingExit, setConfirmingExit] = useState(false)
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
            onOpenChange={setConfirmingExit}
            onConfirm={() => navigate({ name: 'home' })}
          />
          {/* While the question is up it gets the row to itself. The question and its two
              buttons do not fit beside the counter and the chip, and letting them wrap
              tore the header into three lines with the counter stranded in the middle. */}
          {!confirmingExit && (
            <>
              <div class="spacer" />
              <span class="workout-counter">
                {done}/{total}
              </span>
              <SaveStatusChip />
            </>
          )}
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
  const [showGuide, setShowGuide] = useState(false)

  const mine = sets.filter((s) => s.exercise_id === exercise.id)
  const done = doneCount(mine)

  const previous = lastResult(state.sessions, state.sets, exercise.id, sessionID)
  // On an assisted machine the number is the help given, so the best result is the smallest.
  const lowerIsBetter = exercise.lower_is_better === true
  const best = bestWeight(state.sessions, state.sets, exercise.id, sessionID, lowerIsBetter)
  const todayBest = mine.reduce<number | null>(
    (top, s) =>
      s.done && s.weight !== null && (top === null || isBetter(s.weight, top, lowerIsBetter))
        ? s.weight
        : top,
    null,
  )
  const isRecord =
    exercise.weighted &&
    todayBest !== null &&
    (best === null || isBetter(todayBest, best, lowerIsBetter))

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
            previous={previous}
            sessionID={sessionID}
            row={setAt(mine, exercise.id, idx)}
            editing={editing === idx}
            onEdit={setEditing}
          />
        ))}
      </div>

      {/* Both the control and what it opens live below the set row. The card's hierarchy is
          name → scheme → last result → sets, and the reference is none of those: putting the
          button up in the head would wedge it between the scheme and the last result, which
          is the line the user actually reads before choosing today's weight. Down here
          nothing moves under a finger already aiming at a square, either. */}
      <button
        class="guide-toggle"
        aria-expanded={showGuide}
        onClick={() => setShowGuide((open) => !open)}
      >
        {showGuide ? 'Скрыть технику' : 'Как выполнять'}
      </button>

      {showGuide && <ExerciseGuide exerciseID={exercise.id} />}
    </section>
  )
}

function SetColumn({
  idx,
  exercise,
  previous,
  sessionID,
  row,
  editing,
  onEdit,
}: {
  idx: number
  exercise: Exercise
  previous: LastResult | null
  sessionID: string
  row: SetRow | undefined
  editing: boolean
  onEdit: (idx: number | null) => void
}) {
  const done = row?.done ?? false
  // What this very set held last time. It is a hint, never a value: a weight nobody lifted
  // must not end up recorded because a field was pre-filled and then left alone.
  const last = lastSetAt(previous, idx)
  const weightRef = useRef<HTMLInputElement>(null)
  const pressedAt = useRef(0)

  /**
   * Tapping the button IMMEDIATELY writes the mark with the default reps and highlights the
   * button at once, then hands the keyboard to the weight field below it. So the data is
   * saved from the moment of the tap, and the field that opens is the one holding the only
   * number the tap cannot guess.
   *
   * The reps are not that number: they come from the program and are right almost every
   * time. An editor for them opening over the square the finger has just hit is what
   * swallowed a week of weights — everything typed straight after a tap went into it.
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
    // Focused synchronously inside the tap: iOS raises the keyboard only for a focus that
    // happens within the gesture itself, so this must not wait on the write above.
    // An unweighted exercise has no weight field, and there the reps are the whole record —
    // that is the one case where the editor is still what the tap opens.
    if (exercise.weighted) weightRef.current?.focus()
    else onEdit(idx)
  }

  /**
   * Holding a marked set opens the reps editor: the rare correction — six instead of eight —
   * asked for deliberately rather than offered after every tap. A hold on an unmarked set is
   * just a slow tap, because the editor only ever belongs on a set that is already counted.
   */
  const HOLD_MS = 450

  function press() {
    const started = pressedAt.current
    pressedAt.current = 0
    // started === 0 means the activation came from a keyboard, not a finger: no hold to read.
    if (started !== 0 && Date.now() - started >= HOLD_MS && done) {
      onEdit(idx)
      return
    }
    toggle()
  }

  return (
    <div class="set-col">
      {/* The editor takes the button's own place rather than opening below it: the value is
          typed where the finger already is, and the column keeps its height, so nothing
          under it jumps while the keyboard is coming up. */}
      {editing && done ? (
        <RepsEditor
          label={`Повторения, подход ${idx + 1}`}
          /* The unit comes from the program, not from the saved row: it says what this
             exercise is measured in, and that is true of a set nobody has done yet. */
          unit={repsUnit(exercise.default_reps)}
          placeholder={repsCount(row?.reps ?? exercise.default_reps)}
          onDone={(reps) => {
            onEdit(null)
            // An empty field means "leave what the tap wrote": the default is already
            // saved, so the usual case — the default was right — takes no typing and no
            // clearing. Only a typed value is a change worth writing.
            if (reps === '') return
            if (reps !== (row?.reps ?? '')) {
              void upsertSet(sessionID, exercise.id, idx, {
                done: true,
                weight: row?.weight ?? null,
                reps,
              })
            }
          }}
        />
      ) : (
        <button
          class={`set-btn ${done ? 'set-btn-done' : ''}`}
          onPointerDown={() => {
            pressedAt.current = Date.now()
          }}
          onClick={press}
          aria-label={`Подход ${idx + 1}`}
        >
          {done ? (row?.reps ?? exercise.default_reps) : '—'}
        </button>
      )}

      {exercise.weighted && (
        <WeightField
          inputRef={weightRef}
          value={row?.weight ?? null}
          placeholder={last?.weight != null ? fmtWeight(last.weight) : 'кг'}
          /* Not upsertSet with the row this render is holding: the mark written by the tap
             that opened this field may not have landed yet, and setWeight rebuilds the row
             from storage instead of carrying a stale `done` back over it. */
          onCommit={(weight) => {
            void setWeight(sessionID, exercise.id, idx, weight)
          }}
        />
      )}
    </div>
  )
}

function WeightField({
  inputRef,
  value,
  placeholder,
  onCommit,
}: {
  inputRef: RefObject<HTMLInputElement>
  value: number | null
  placeholder: string
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
      ref={inputRef}
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
      /* Last time's weight for this very set, greyed out — the number today's decision is
         made from, in the place the decision is typed. «кг» when there is no last time. */
      placeholder={placeholder}
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

/**
 * The reps editor.
 *
 * It stands exactly where the set button was, in the same square, and opens EMPTY with the
 * default shown as a placeholder rather than as text. The default is already saved by the
 * tap, so the common case — it was right — needs no typing, and the rare case needs no
 * clearing first. Pre-filled text would have to be deleted every time it is wrong, which is
 * the one moment when hands are chalked and patience is short.
 *
 * The field takes digits only. Where the exercise is measured in something else — «30с»,
 * «40м», «10/нога» — the unit is printed under the number and appended on save, so the
 * stored value keeps the shape it has always had while the keyboard stays a keypad.
 */
function RepsEditor({
  label,
  unit,
  placeholder,
  onDone,
}: {
  label: string
  unit: string
  placeholder: string
  onDone: (reps: string) => void
}) {
  const [text, setText] = useState('')
  const ref = useRef<HTMLInputElement>(null)

  useEffect(() => {
    ref.current?.focus()
  }, [])

  return (
    <div class="reps-box">
      <input
        ref={ref}
        /* Still type="text": type="number" hands back an empty string for anything it does
           not like, which is how a field silently loses what was typed — the same trap as
           the comma in the weight field above. The digits-only rule lives in onInput.
           inputmode is "decimal" rather than "numeric" even though reps are whole numbers,
           so that every field on this screen asks for the same keyboard. iOS presents the
           keypad for the field taking focus, and moving straight from here to the weight
           field — tapping it while this editor is still open — does not reliably re-present
           it; a digits-only pad left standing over the weight field is a field that cannot
           take 28,5. */
        type="text"
        inputMode="decimal"
        enterKeyHint="done"
        autocomplete="off"
        class="reps-field"
        aria-label={label}
        placeholder={placeholder}
        value={text}
        onInput={(e) => {
          const el = e.currentTarget as HTMLInputElement
          const digits = el.value.replace(/\D/g, '').slice(0, 4)
          // Written back to the element directly, not just to state: when the cleaned value
          // equals what state already holds there is no re-render to undo the character the
          // field is showing.
          if (digits !== el.value) el.value = digits
          setText(digits)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') onDone(joinReps(text, unit))
        }}
        onBlur={() => onDone(joinReps(text, unit))}
      />
      {/* Not read out: the field's own label already says what is being entered, and the
          unit repeated after every number turns the screen reader into a metronome. */}
      {unit !== '' && (
        <span class="reps-unit" aria-hidden="true">
          {unit}
        </span>
      )}
    </div>
  )
}
