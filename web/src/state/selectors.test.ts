import { describe, expect, it } from 'vitest'
import {
  bestWeight,
  chartableExercises,
  draft,
  history,
  lastDoneAt,
  lastResult,
  progressSeries,
  totalSets,
} from './selectors'
import type { Program, SessionRow, SetRow } from '../types'

const DAY = 86_400_000

function session(id: string, overrides: Partial<SessionRow> = {}): SessionRow {
  return {
    id,
    date: '2026-07-28',
    day_id: 'd1',
    program_hash: 'hash-a',
    started_at: 1000,
    finished_at: 2000,
    deleted: false,
    note: '',
    updated_ts: 1000,
    updated_by: 'phone',
    rev: 1,
    ...overrides,
  }
}

function set(sessionID: string, exerciseID: string, idx: number, overrides: Partial<SetRow> = {}): SetRow {
  return {
    session_id: sessionID,
    exercise_id: exerciseID,
    idx,
    done: true,
    weight: 80,
    reps: '5',
    deleted: false,
    updated_ts: 1000,
    updated_by: 'phone',
    rev: 1,
    ...overrides,
  }
}

const program: Program = {
  version: 1,
  name: 'Тест',
  days: [
    {
      id: 'd1',
      name: 'Жим',
      muscles: 'Грудь',
      exercises: [
        { id: 'bench_bb', name: 'Жим лёжа', scheme: '4×5', sets: 4, default_reps: '5', weighted: true },
        { id: 'plank', name: 'Планка', scheme: '3×40 с', sets: 3, default_reps: '40с', weighted: false },
      ],
    },
  ],
}

describe('история и черновик', () => {
  it('в историю попадают только завершённые и не удалённые', () => {
    const sessions = [
      session('s1'),
      session('s2', { finished_at: null }),
      session('s3', { deleted: true }),
    ]
    expect(history(sessions).map((s) => s.id)).toEqual(['s1'])
  })

  it('черновик — незавершённая и не удалённая тренировка', () => {
    const sessions = [session('s1'), session('s2', { finished_at: null, started_at: 5000 })]
    expect(draft(sessions)?.id).toBe('s2')
  })

  it('удалённый черновик не показывается', () => {
    const sessions = [session('s2', { finished_at: null, deleted: true })]
    expect(draft(sessions)).toBeNull()
  })
})

describe('прошлый результат', () => {
  /**
   * The key element of the workout screen: it is what today's weight is decided from.
   * It has to take the previous workout, not the current one.
   */
  it('берётся из предыдущей тренировки, а не из текущей', () => {
    const sessions = [
      session('старая', { started_at: 1 * DAY }),
      session('текущая', { started_at: 2 * DAY, finished_at: null }),
    ]
    const sets = [
      set('старая', 'bench_bb', 0, { weight: 80, reps: '5' }),
      set('старая', 'bench_bb', 1, { weight: 82.5, reps: '4' }),
      set('текущая', 'bench_bb', 0, { weight: 90, reps: '3' }),
    ]

    const result = lastResult(sessions, sets, 'bench_bb', 'текущая')
    expect(result?.at).toBe(1 * DAY)
    expect(result?.sets).toEqual([
      { weight: 80, reps: '5' },
      { weight: 82.5, reps: '4' },
    ])
  })

  it('пропускает тренировки без этого упражнения', () => {
    const sessions = [
      session('давняя', { started_at: 1 * DAY }),
      session('недавняя', { started_at: 2 * DAY }),
      session('текущая', { started_at: 3 * DAY, finished_at: null }),
    ]
    const sets = [
      set('давняя', 'bench_bb', 0, { weight: 75 }),
      set('недавняя', 'squat_bb', 0, { weight: 100 }),
    ]
    expect(lastResult(sessions, sets, 'bench_bb', 'текущая')?.at).toBe(1 * DAY)
  })

  it('не берёт данные из удалённой тренировки', () => {
    const sessions = [
      session('удалённая', { started_at: 1 * DAY, deleted: true }),
      session('текущая', { started_at: 2 * DAY, finished_at: null }),
    ]
    const sets = [set('удалённая', 'bench_bb', 0, { weight: 80 })]
    expect(lastResult(sessions, sets, 'bench_bb', 'текущая')).toBeNull()
  })

  it('неотмеченные подходы в прошлый результат не входят', () => {
    const sessions = [
      session('прошлая', { started_at: 1 * DAY }),
      session('текущая', { started_at: 2 * DAY, finished_at: null }),
    ]
    const sets = [
      set('прошлая', 'bench_bb', 0, { weight: 80 }),
      set('прошлая', 'bench_bb', 1, { done: false, weight: 80 }),
    ]
    expect(lastResult(sessions, sets, 'bench_bb', 'текущая')?.sets).toHaveLength(1)
  })
})

describe('исторический максимум', () => {
  it('считается без учёта текущей тренировки — иначе рекорд не с чем сравнивать', () => {
    const sessions = [
      session('прошлая', { started_at: 1 * DAY }),
      session('текущая', { started_at: 2 * DAY, finished_at: null }),
    ]
    const sets = [
      set('прошлая', 'bench_bb', 0, { weight: 80 }),
      set('прошлая', 'bench_bb', 1, { weight: 85 }),
      set('текущая', 'bench_bb', 0, { weight: 100 }),
    ]
    expect(bestWeight(sessions, sets, 'bench_bb', 'текущая')).toBe(85)
  })

  it('без истории максимума нет — первая тренировка не объявляется рекордом', () => {
    const sessions = [session('текущая', { finished_at: null })]
    const sets = [set('текущая', 'bench_bb', 0, { weight: 100 })]
    expect(bestWeight(sessions, sets, 'bench_bb', 'текущая')).toBeNull()
  })

  it('упражнения без веса максимума не дают', () => {
    const sessions = [session('прошлая')]
    const sets = [set('прошлая', 'plank', 0, { weight: null, reps: '40с' })]
    expect(bestWeight(sessions, sets, 'plank', 'текущая')).toBeNull()
  })
})

describe('ряд для графика', () => {
  it('берёт лучший рабочий вес каждой тренировки по возрастанию даты', () => {
    const sessions = [
      session('s2', { started_at: 2 * DAY }),
      session('s1', { started_at: 1 * DAY }),
    ]
    const sets = [
      set('s1', 'bench_bb', 0, { weight: 80 }),
      set('s1', 'bench_bb', 1, { weight: 82.5 }),
      set('s2', 'bench_bb', 0, { weight: 85 }),
    ]
    expect(progressSeries(sessions, sets, 'bench_bb')).toEqual([
      { at: 1 * DAY, weight: 82.5, sessionID: 's1' },
      { at: 2 * DAY, weight: 85, sessionID: 's2' },
    ])
  })

  it('незавершённая тренировка в график не попадает', () => {
    const sessions = [session('s1'), session('s2', { finished_at: null, started_at: 2 * DAY })]
    const sets = [set('s1', 'bench_bb', 0), set('s2', 'bench_bb', 0, { weight: 200 })]
    expect(progressSeries(sessions, sets, 'bench_bb')).toHaveLength(1)
  })

  it('график рисуется только при двух и более тренировках', () => {
    const sessions = [session('s1'), session('s2', { started_at: 2 * DAY })]
    const one = [set('s1', 'bench_bb', 0)]
    const two = [...one, set('s2', 'bench_bb', 0, { weight: 85 })]
    const programs = new Map([['hash-a', program]])

    expect(chartableExercises(sessions, one, programs)).toHaveLength(0)
    expect(chartableExercises(sessions, two, programs)).toHaveLength(1)
  })

  it('упражнения без веса в графики не попадают', () => {
    const sessions = [session('s1'), session('s2', { started_at: 2 * DAY })]
    const sets = [
      set('s1', 'plank', 0, { weight: null, reps: '40с' }),
      set('s2', 'plank', 0, { weight: null, reps: '40с' }),
    ]
    const programs = new Map([['hash-a', program]])
    expect(chartableExercises(sessions, sets, programs)).toHaveLength(0)
  })
})

describe('прочее', () => {
  it('знаменатель счётчика берётся из программы этой тренировки', () => {
    expect(totalSets(program, 'd1')).toBe(7)
    expect(totalSets(program, 'нет-такого')).toBe(0)
    expect(totalSets(null, 'd1')).toBe(0)
  })

  it('последнее выполнение дня — только по завершённым тренировкам', () => {
    const sessions = [
      session('s1', { day_id: 'd1', started_at: 1 * DAY }),
      session('s2', { day_id: 'd1', started_at: 3 * DAY, finished_at: null }),
      session('s3', { day_id: 'd2', started_at: 5 * DAY }),
    ]
    expect(lastDoneAt(sessions, 'd1')).toBe(1 * DAY)
    expect(lastDoneAt(sessions, 'd3')).toBeNull()
  })
})
