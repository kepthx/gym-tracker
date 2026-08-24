import { describe, expect, it } from 'vitest'
import {
  bestWeight,
  chartableExercises,
  isBetter,
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

describe('направление прогресса (тренажёр с противовесом)', () => {
  // На гравитроне записывается помощь тренажёра: 30 кг лучше, чем 35. Если этого не
  // учитывать, приложение поздравляет с рекордом за шаг назад.
  const assisted: Program = {
    version: 1,
    name: 'Тест',
    days: [
      {
        id: 'd2',
        name: 'Тяга',
        muscles: 'Спина',
        exercises: [
          {
            id: 'pullup_assisted',
            name: 'Подтягивания в гравитроне',
            scheme: '4×8–10',
            sets: 4,
            default_reps: '8',
            weighted: true,
            lower_is_better: true,
          },
        ],
      },
    ],
  }

  it('isBetter разворачивается по флагу', () => {
    expect(isBetter(85, 80)).toBe(true)
    expect(isBetter(80, 85)).toBe(false)
    expect(isBetter(30, 35, true)).toBe(true)
    expect(isBetter(35, 30, true)).toBe(false)
  })

  it('лучший результат — наименьший вес', () => {
    const sessions = [session('s1'), session('s2')]
    const sets = [
      set('s1', 'pullup_assisted', 0, { weight: 35 }),
      set('s2', 'pullup_assisted', 0, { weight: 30 }),
    ]
    expect(bestWeight(sessions, sets, 'pullup_assisted', 'none', true)).toBe(30)
    // Без флага та же выборка даёт противоположный ответ — это и была ошибка.
    expect(bestWeight(sessions, sets, 'pullup_assisted', 'none')).toBe(35)
  })

  it('в серии за тренировку берётся наименьший вес', () => {
    const sessions = [session('s1')]
    const sets = [
      set('s1', 'pullup_assisted', 0, { weight: 35 }),
      set('s1', 'pullup_assisted', 1, { weight: 30 }),
    ]
    expect(progressSeries(sessions, sets, 'pullup_assisted', true)[0]!.weight).toBe(30)
    expect(progressSeries(sessions, sets, 'pullup_assisted')[0]!.weight).toBe(35)
  })

  it('chartableExercises переносит направление из программы', () => {
    const sessions = [session('s1'), session('s2', { started_at: 2000 })]
    const sets = [
      set('s1', 'pullup_assisted', 0, { weight: 35 }),
      set('s2', 'pullup_assisted', 0, { weight: 30 }),
    ]
    const charts = chartableExercises(sessions, sets, new Map([['hash-a', assisted]]))
    expect(charts).toHaveLength(1)
    expect(charts[0]!.lowerIsBetter).toBe(true)
  })

  it('у обычного упражнения направление остаётся прежним', () => {
    const sessions = [session('s1'), session('s2', { started_at: 2000 })]
    const sets = [
      set('s1', 'bench_bb', 0, { weight: 80 }),
      set('s2', 'bench_bb', 0, { weight: 85 }),
    ]
    const charts = chartableExercises(sessions, sets, new Map([['hash-a', program]]))
    expect(charts[0]!.lowerIsBetter).toBe(false)
    expect(bestWeight(sessions, sets, 'bench_bb', 'none')).toBe(85)
  })
})
