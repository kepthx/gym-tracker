const MONTHS = [
  'янв',
  'фев',
  'мар',
  'апр',
  'мая',
  'июн',
  'июл',
  'авг',
  'сен',
  'окт',
  'ноя',
  'дек',
]

/** Short date: «14 июл». The year is added only when it is not the current one. */
export function fmtDate(at: number, now = Date.now()): string {
  const d = new Date(at)
  const base = `${d.getDate()} ${MONTHS[d.getMonth()]}`
  return d.getFullYear() === new Date(now).getFullYear() ? base : `${base} ${d.getFullYear()}`
}

export function fmtTime(at: number): string {
  const d = new Date(at)
  return `${d.getHours()}:${String(d.getMinutes()).padStart(2, '0')}`
}

/** «сегодня в 18:40», «вчера», «14 июл» — for the unfinished-workout card. */
export function fmtStarted(at: number, now = Date.now()): string {
  const start = new Date(at)
  const today = new Date(now)
  const sameDay =
    start.getFullYear() === today.getFullYear() &&
    start.getMonth() === today.getMonth() &&
    start.getDate() === today.getDate()
  if (sameDay) return `сегодня в ${fmtTime(at)}`

  const yesterday = new Date(now - 86400_000)
  const wasYesterday =
    start.getFullYear() === yesterday.getFullYear() &&
    start.getMonth() === yesterday.getMonth() &&
    start.getDate() === yesterday.getDate()
  if (wasYesterday) return `вчера в ${fmtTime(at)}`

  return `${fmtDate(at, now)} в ${fmtTime(at)}`
}

/** Weight in Russian style: comma as the separator, no trailing zeros. */
export function fmtWeight(kg: number | null): string {
  if (kg === null) return ''
  return String(Math.round(kg * 100) / 100).replace('.', ',')
}

/**
 * Parses a weight from the input field.
 *
 * Comma and period behave identically: on a Russian layout the iOS numeric keyboard
 * produces a comma, and a field that does not accept it silently loses what was typed.
 */
export function parseWeight(raw: string): number | null {
  const trimmed = raw.trim().replace(',', '.')
  if (trimmed === '') return null
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0 || value > 10000) return null
  return value
}

/** Live input validation: an empty string and a half-typed number are both allowed. */
export function isWeightInputValid(raw: string): boolean {
  return /^\d{0,4}([.,]\d{0,2})?$/.test(raw.trim())
}

/**
 * Whether an exercise's reps are a plain count, and so can have a digits-only keyboard.
 *
 * Not every exercise's are: a plank is «30с», a carry «40м», a lunge «10/нога». Those need
 * letters and a slash, and a numeric keypad would make them impossible to type. The program's
 * default is what decides, because it is what the field is pre-filled with.
 */
export function isNumericReps(reps: string): boolean {
  return /^\d+$/.test(reps.trim())
}

/** The last-result line: «80×8 · 80×8 · 82,5×6». */
export function fmtSets(sets: { weight: number | null; reps: string | null }[]): string {
  return sets
    .map((s) => {
      const reps = s.reps ?? ''
      if (s.weight === null) return reps || '—'
      return reps ? `${fmtWeight(s.weight)}×${reps}` : fmtWeight(s.weight)
    })
    .join(' · ')
}

/** Russian numeral agreement: «1 тренировка», «2 тренировки», «5 тренировок». */
export function plural(n: number, one: string, few: string, many: string): string {
  const mod100 = n % 100
  if (mod100 >= 11 && mod100 <= 14) return many
  switch (n % 10) {
    case 1:
      return one
    case 2:
    case 3:
    case 4:
      return few
    default:
      return many
  }
}

export function workoutsRecorded(n: number): string {
  return `${n} ${plural(n, 'тренировка', 'тренировки', 'тренировок')} записано`
}
