import { describe, expect, it } from 'vitest'
import { joinReps, repsCount, repsUnit } from './format'

/**
 * Splitting a reps value into what the user types and what the exercise says.
 *
 * The field takes digits and nothing else, so the unit has to come off before the value
 * reaches it and go back on before the value is stored: history has always held «30с», and
 * a set recorded today has to read the same as one recorded a year ago.
 */
describe('repsCount / repsUnit', () => {
  it('обычный счёт повторений — единицы нет', () => {
    for (const reps of ['8', '12', '100']) {
      expect(repsCount(reps), reps).toBe(reps)
      expect(repsUnit(reps), reps).toBe('')
    }
  })

  it('время, расстояние и «на ногу» — число отдельно, единица отдельно', () => {
    // Every one of these is in the user's own program.
    expect(repsCount('30с')).toBe('30')
    expect(repsUnit('30с')).toBe('с')
    expect(repsCount('40м')).toBe('40')
    expect(repsUnit('40м')).toBe('м')
    expect(repsCount('10/нога')).toBe('10')
    expect(repsUnit('10/нога')).toBe('/нога')
  })

  it('пробелы вокруг значения не мешают', () => {
    expect(repsCount(' 8 ')).toBe('8')
    expect(repsUnit(' 30с ')).toBe('с')
  })

  it('пустое значение не выдумывает числа', () => {
    expect(repsCount('')).toBe('')
    expect(repsUnit('')).toBe('')
  })

  it('разбор и сборка возвращают исходное значение', () => {
    for (const reps of ['8', '30с', '40м', '10/нога']) {
      expect(joinReps(repsCount(reps), repsUnit(reps)), reps).toBe(reps)
    }
  })

  it('пустое поле остаётся пустым, а не превращается в одну единицу', () => {
    // «с» вместо числа попало бы в историю как выполненный подход без результата.
    expect(joinReps('', 'с')).toBe('')
  })
})
