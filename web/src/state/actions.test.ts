import { beforeEach, describe, expect, it } from 'vitest'
import { getSet, resetForTests, setMeta } from '../db/idb'
import { initActions, setWeight, upsertSet } from './actions'

const S = 'сессия-1'
const EX = 'press_db_incline'

beforeEach(async () => {
  resetForTests()
  await setMeta('device_id', 'устройство-1')
  await initActions()
})

/**
 * The order that broke a week of records.
 *
 * Tapping a set marks it; the keyboard then lands on the weight field of that same column,
 * and the weight is written straight after. Both writes carry the whole row, so the second
 * one decides what the first one's fields end up being.
 */
describe('setWeight', () => {
  it('не снимает отметку, поставленную мгновением раньше', async () => {
    // Deliberately not awaited: this is the tap, and the weight follows it without waiting.
    void upsertSet(S, EX, 0, { done: true, weight: null, reps: '10' })
    await setWeight(S, EX, 0, 28.5)

    const row = await getSet(S, EX, 0)
    expect(row?.done, 'подход обязан остаться отмеченным').toBe(true)
    expect(row?.weight).toBe(28.5)
    expect(row?.reps, 'повторения от тапа не должны потеряться').toBe('10')
  })

  it('на неотмеченном подходе просто записывает вес', async () => {
    await setWeight(S, EX, 1, 30)

    const row = await getSet(S, EX, 1)
    expect(row?.done).toBe(false)
    expect(row?.weight).toBe(30)
    expect(row?.reps).toBeNull()
  })

  it('очистка поля стирает вес, не трогая остального', async () => {
    await upsertSet(S, EX, 2, { done: true, weight: 28.5, reps: '10' })
    await setWeight(S, EX, 2, null)

    const row = await getSet(S, EX, 2)
    expect(row?.weight).toBeNull()
    expect(row?.done).toBe(true)
    expect(row?.reps).toBe('10')
  })
})
