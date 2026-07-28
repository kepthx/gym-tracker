import { beforeEach, describe, expect, it, vi } from 'vitest'
import { applyLocal, deadLetters, getMeta, openDB, outboxHead, outboxSize, resetForTests } from '../db/idb'
import type { Op, SetRow, SyncResponse } from '../types'

const postSync = vi.fn()
const getSync = vi.fn()

vi.mock('./client', async () => {
  const actual = await vi.importActual<typeof import('./client')>('./client')
  return {
    ...actual,
    postSync: (...args: unknown[]) => postSync(...args),
    getSync: (...args: unknown[]) => getSync(...args),
  }
})

const { ApiError, OfflineError } = await import('./client')
const { SyncEngine } = await import('./engine')

function op(id: string, idx: number): Op {
  return {
    op_id: id,
    ts: 1000,
    seq: idx,
    type: 'set.upsert',
    session_id: 's1',
    exercise_id: 'bench_bb',
    idx,
    done: true,
    weight: 80,
    reps: '5',
  }
}

function setRow(idx: number): SetRow {
  return {
    session_id: 's1',
    exercise_id: 'bench_bb',
    idx,
    done: true,
    weight: 80,
    reps: '5',
    deleted: false,
    updated_ts: 1000,
    updated_by: 'phone',
    rev: 0,
  }
}

function emptyResponse(cursor = 10): SyncResponse {
  return {
    cursor,
    results: [],
    changes: { sessions: [], sets: [], programs: [] },
    has_more: false,
    server_time: Date.now(),
  }
}

async function seedQueue(count: number): Promise<void> {
  for (let i = 0; i < count; i++) {
    await applyLocal(op(`op-${i}`, i), ({ sets }) => sets.put(setRow(i)))
  }
}

/** An engine with no triggers installed: the tests call flush directly. */
function newEngine() {
  const engine = new SyncEngine()
  // The request needs deviceID, but installTriggers and its timers are not wanted in a test.
  Object.assign(engine as unknown as { deviceID: string }, { deviceID: 'test-device' })
  return engine
}

beforeEach(async () => {
  postSync.mockReset()
  getSync.mockReset()
  resetForTests()
  indexedDB.deleteDatabase('gymtracker')
  await new Promise((resolve) => setTimeout(resolve, 0))
  await openDB()
})

describe('успешная отправка', () => {
  it('очищает очередь, двигает курсор и показывает «сохранено»', async () => {
    await seedQueue(3)
    postSync.mockResolvedValue({
      ...emptyResponse(77),
      results: [
        { op_id: 'op-0', status: 'applied' },
        { op_id: 'op-1', status: 'applied' },
        { op_id: 'op-2', status: 'duplicate' },
      ],
    })

    const engine = newEngine()
    await engine.flush()

    expect(await outboxSize()).toBe(0)
    expect(await getMeta<number>('cursor')).toBe(77)
    expect(engine.getStatus().state).toBe('synced')
  })

  it('операция без вердикта остаётся в очереди', async () => {
    await seedQueue(2)
    postSync.mockResolvedValue({
      ...emptyResponse(),
      results: [{ op_id: 'op-0', status: 'applied' }],
    })

    await newEngine().flush()

    const rest = await outboxHead(10)
    expect(rest.map((e) => e.op.op_id)).toEqual(['op-1'])
  })
})

describe('нет связи', () => {
  /**
   * A queue with no signal is an expected state at the gym, not a failure. It is shown as
   * "saved on device"; otherwise people stop believing the indicator, and the "visible save
   * status" requirement dies along with that trust.
   */
  it('показывает «сохранено на устройстве» и не теряет очередь', async () => {
    await seedQueue(4)
    postSync.mockRejectedValue(new OfflineError())

    const engine = newEngine()
    await engine.flush()

    expect(engine.getStatus().state).toBe('local')
    expect(engine.getStatus().pending).toBe(4)
    expect(await outboxSize()).toBe(4)
    engine.stop()
  })
})

describe('истёкший вход', () => {
  /**
   * The most dangerous reaction to a 401 is clearing the queue on the grounds that it was
   * not accepted anyway. A recorded workout is sitting in it, and losing that is unacceptable.
   */
  it('переходит в «требуется вход» и СОХРАНЯЕТ очередь', async () => {
    await seedQueue(5)
    postSync.mockRejectedValue(new ApiError(401, 'unauthorized', 'требуется вход'))

    const engine = newEngine()
    await engine.flush()

    expect(engine.getStatus().state).toBe('auth')
    expect(await outboxSize()).toBe(5)
    const queue = await outboxHead(10)
    expect(queue.map((e) => e.op.op_id)).toEqual(['op-0', 'op-1', 'op-2', 'op-3', 'op-4'])
  })

  /**
   * The "login required" banner has to disappear after a successful sync. Otherwise it
   * hangs forever, the user stops noticing it — and will not notice it when it appears for
   * a real reason.
   */
  it('после удачной отправки полоса «требуется вход» снимается', async () => {
    await seedQueue(1)
    postSync.mockRejectedValueOnce(new ApiError(401, 'unauthorized', 'требуется вход'))

    const engine = newEngine()
    await engine.flush()
    expect(engine.getStatus().state).toBe('auth')

    postSync.mockResolvedValue({
      ...emptyResponse(),
      results: [{ op_id: 'op-0', status: 'applied' }],
    })
    await engine.flush()

    expect(engine.getStatus().state).toBe('synced')
  })

  it('после входа очередь уезжает целиком', async () => {
    await seedQueue(2)
    postSync.mockRejectedValueOnce(new ApiError(401, 'unauthorized', 'требуется вход'))

    const engine = newEngine()
    await engine.flush()
    expect(await outboxSize()).toBe(2)

    postSync.mockResolvedValue({
      ...emptyResponse(),
      results: [
        { op_id: 'op-0', status: 'applied' },
        { op_id: 'op-1', status: 'applied' },
      ],
    })
    await engine.flush()

    expect(await outboxSize()).toBe(0)
    expect(engine.getStatus().state).toBe('synced')
  })
})

describe('отказ сервера', () => {
  it('поднимает тревогу только после нескольких отказов подряд', async () => {
    await seedQueue(1)
    postSync.mockRejectedValue(new ApiError(500, 'internal', 'не удалось сохранить'))

    const engine = newEngine()

    await engine.flush()
    expect(engine.getStatus().state).toBe('local')
    await engine.flush()
    expect(engine.getStatus().state).toBe('local')
    await engine.flush()
    // A one-off failure is no reason to alarm anyone: the alarm turns on when it persists.
    expect(engine.getStatus().state).toBe('error')

    expect(await outboxSize()).toBe(1)
    engine.stop()
  })
})

describe('отклонённые операции', () => {
  /**
   * One broken operation must not hold up the queue: otherwise the user loses everything
   * behind it. It moves to a separate store and raises a conspicuous red banner — discarding
   * it silently is not an option either.
   */
  it('уезжают в отдельное хранилище, остальные проходят, статус — ошибка', async () => {
    await seedQueue(3)
    postSync.mockResolvedValue({
      ...emptyResponse(),
      results: [
        { op_id: 'op-0', status: 'applied' },
        { op_id: 'op-1', status: 'rejected', reason: 'exercise_id не подходит под формат' },
        { op_id: 'op-2', status: 'applied' },
      ],
    })

    const engine = newEngine()
    await engine.flush()

    expect(await outboxSize()).toBe(0)
    const dead = await deadLetters()
    expect(dead).toHaveLength(1)
    expect(dead[0]!.op.op_id).toBe('op-1')
    expect(dead[0]!.reason).toContain('exercise_id')
    expect(engine.getStatus().state).toBe('error')
    expect(engine.getStatus().dead).toBe(1)
  })
})

describe('пустая очередь', () => {
  it('всё равно забирает дельту с сервера', async () => {
    getSync.mockResolvedValue({
      ...emptyResponse(5),
      changes: {
        sessions: [],
        sets: [setRow(0)],
        programs: [],
      },
    })

    const engine = newEngine()
    await engine.flush()

    expect(getSync).toHaveBeenCalledOnce()
    expect(postSync).not.toHaveBeenCalled()
    expect(await getMeta<number>('cursor')).toBe(5)
    expect(engine.getStatus().state).toBe('synced')
  })
})
