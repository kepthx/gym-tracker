import { describe, expect, it } from 'vitest'
import { isNumericReps } from './format'

/**
 * Which exercises may have a digits-only keyboard.
 *
 * The field itself stays type="text" whatever this returns — type="number" hands back an
 * empty value for anything it does not consider a number, which is how a field silently
 * loses what was typed. This only picks the keyboard.
 */
describe('isNumericReps', () => {
  it('обычный счёт повторений — цифровая клавиатура', () => {
    for (const reps of ['8', '12', '5', '100']) {
      expect(isNumericReps(reps), reps).toBe(true)
    }
  })

  it('всё, что не сводится к числу, оставляет обычную клавиатуру', () => {
    // Every one of these is in the user's own program: without letters and a slash they
    // could not be typed at all.
    for (const reps of ['30с', '40м', '10/нога', '8-10', '', '  ']) {
      expect(isNumericReps(reps), reps).toBe(false)
    }
  })

  it('пробелы вокруг числа не мешают', () => {
    expect(isNumericReps(' 8 ')).toBe(true)
  })
})
