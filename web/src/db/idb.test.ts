import { beforeEach, describe, expect, it } from 'vitest'
import {
  applyLocal,
  applyRemote,
  dropFromOutbox,
  moveToDeadLetter,
  openDB,
  outboxHead,
  outboxSize,
  resetForTests,
  allSets,
  allSessions,
  getMeta,
  clearUserData,
  deadLetters,
} from './idb'
import { mergeSession, mergeSet } from './merge'
import type { Op, SessionRow, SetRow } from '../types'

function op(id: string): Op {
  return {
    op_id: id,
    ts: 1000,
    seq: 1,
    type: 'set.upsert',
    session_id: 's1',
    exercise_id: 'bench_bb',
    idx: 0,
    done: true,
    weight: 80,
    reps: '5',
  }
}

function setRow(overrides: Partial<SetRow> = {}): SetRow {
  return {
    session_id: 's1',
    exercise_id: 'bench_bb',
    idx: 0,
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

function sessionRow(overrides: Partial<SessionRow> = {}): SessionRow {
  return {
    id: 's1',
    date: '2026-07-28',
    day_id: 'd1',
    program_hash: 'hash-a',
    started_at: 1000,
    finished_at: null,
    deleted: false,
    note: '',
    updated_ts: 1000,
    updated_by: 'phone',
    rev: 1,
    ...overrides,
  }
}

beforeEach(async () => {
  resetForTests()
  indexedDB.deleteDatabase('gymtracker')
  // Wait until the deletion has actually taken effect.
  await new Promise((resolve) => setTimeout(resolve, 0))
  await openDB()
})

describe('атомарность записи', () => {
  it('состояние и очередь пишутся одной транзакцией', async () => {
    await applyLocal(op('op-1'), ({ sets }) => sets.put(setRow()))

    expect(await outboxSize()).toBe(1)
    expect(await allSets()).toHaveLength(1)
  })

  /**
   * The client's most dangerous bug: state and queue drifting apart. It produces either a
   * lost write (a set was marked, never reached the server, and is not there locally) or a
   * phantom sync. The transaction has to roll back both.
   */
  it('исключение посреди записи не оставляет ни состояния, ни очереди', async () => {
    await expect(
      applyLocal(op('op-2'), ({ sets }) => {
        sets.put(setRow())
        throw new Error('подстроенный сбой между двумя записями')
      }),
    ).rejects.toThrow('подстроенный сбой')

    expect(await allSets()).toHaveLength(0)
    expect(await outboxSize()).toBe(0)
  })

  it('битая запись отменяет транзакцию целиком', async () => {
    await expect(
      applyLocal(op('op-3'), ({ sets }) => {
        sets.put(setRow())
        // A set's key is composite; a row without exercise_id cannot form one.
        sets.put({ session_id: 's1' } as unknown as SetRow)
      }),
    ).rejects.toBeTruthy()

    expect(await allSets()).toHaveLength(0)
    expect(await outboxSize()).toBe(0)
  })
})

describe('очередь', () => {
  it('разбирается в порядке добавления', async () => {
    await applyLocal(op('op-a'), ({ sets }) => sets.put(setRow({ idx: 0 })))
    await applyLocal(op('op-b'), ({ sets }) => sets.put(setRow({ idx: 1 })))
    await applyLocal(op('op-c'), ({ sets }) => sets.put(setRow({ idx: 2 })))

    const head = await outboxHead(10)
    expect(head.map((e) => e.op.op_id)).toEqual(['op-a', 'op-b', 'op-c'])
  })

  it('отправленное убирается, остальное остаётся', async () => {
    await applyLocal(op('op-a'), ({ sets }) => sets.put(setRow({ idx: 0 })))
    await applyLocal(op('op-b'), ({ sets }) => sets.put(setRow({ idx: 1 })))

    const head = await outboxHead(10)
    await dropFromOutbox([head[0]!.seq])

    const rest = await outboxHead(10)
    expect(rest.map((e) => e.op.op_id)).toEqual(['op-b'])
  })

  /**
   * A rejected operation has to leave the queue: keeping it there would jam the queue
   * forever and lose everything standing behind it.
   */
  it('отклонённое переезжает в отдельное хранилище и не держит очередь', async () => {
    await applyLocal(op('op-a'), ({ sets }) => sets.put(setRow({ idx: 0 })))
    await applyLocal(op('op-b'), ({ sets }) => sets.put(setRow({ idx: 1 })))

    const head = await outboxHead(10)
    await moveToDeadLetter([{ seq: head[0]!.seq, op: head[0]!.op, reason: 'битый id' }], 5000)

    expect((await outboxHead(10)).map((e) => e.op.op_id)).toEqual(['op-b'])
    const dead = await deadLetters()
    expect(dead).toHaveLength(1)
    expect(dead[0]!.reason).toBe('битый id')
  })
})

describe('применение дельты с сервера', () => {
  it('сливает строки и двигает курсор одной транзакцией', async () => {
    await applyRemote({
      sessions: [sessionRow()],
      sets: [setRow()],
      programs: [{ hash: 'hash-a', json: { version: 1, name: 'Тест', days: [] } }],
      cursor: 42,
      mergeSession,
      mergeSet,
    })

    expect(await allSessions()).toHaveLength(1)
    expect(await allSets()).toHaveLength(1)
    expect(await getMeta<number>('cursor')).toBe(42)
  })

  it('поздняя запись сервера перекрывает локальную, ранняя — нет', async () => {
    await applyRemote({
      sessions: [],
      sets: [setRow({ weight: 80, updated_ts: 1000 })],
      programs: [],
      cursor: 1,
      mergeSession,
      mergeSet,
    })

    await applyRemote({
      sessions: [],
      sets: [setRow({ weight: 90, updated_ts: 2000 })],
      programs: [],
      cursor: 2,
      mergeSession,
      mergeSet,
    })
    expect((await allSets())[0]!.weight).toBe(90)

    await applyRemote({
      sessions: [],
      sets: [setRow({ weight: 70, updated_ts: 500 })],
      programs: [],
      cursor: 3,
      mergeSession,
      mergeSet,
    })
    expect((await allSets())[0]!.weight).toBe(90)
  })

  it('дельта из многих строк применяется целиком', async () => {
    const many = Array.from({ length: 30 }, (_, i) => setRow({ idx: i }))
    await applyRemote({
      sessions: [sessionRow()],
      sets: many,
      programs: [],
      cursor: 99,
      mergeSession,
      mergeSet,
    })
    expect(await allSets()).toHaveLength(30)
    expect(await getMeta<number>('cursor')).toBe(99)
  })
})

describe('выход из аккаунта', () => {
  /**
   * History is inviolable: an unsent workout may be sitting in the queue, and logging out
   * is no reason to lose it.
   */
  it('стирает данные, но не трогает очередь неотправленного', async () => {
    await applyLocal(op('op-a'), ({ sessions, sets }) => {
      sessions.put(sessionRow())
      sets.put(setRow())
    })

    await clearUserData()

    expect(await allSessions()).toHaveLength(0)
    expect(await allSets()).toHaveLength(0)
    expect(await outboxSize()).toBe(1)
  })
})
