import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { mergeSession, mergeSet, newer } from './merge'
import type { SessionRow, SetRow } from '../types'

/**
 * The same truth table the Go tests read (internal/store/rowmerge_test.go).
 *
 * Code cannot be shared between Go and TypeScript, but the truth can. A divergence between
 * the two merge implementations is the most dangerous bug in the system, and one file for
 * both sides catches it from both at once.
 */
const cases = JSON.parse(readFileSync(new URL('../../../testdata/merge_cases.json', import.meta.url), 'utf8')) as {
  lww: { name: string; ts: number; device: string; cur_ts: number; cur_device: string; newer: boolean }[]
  sets: { name: string; current: SetRow | null; incoming: SetRow; expected: SetRow }[]
  sessions: { name: string; current: SessionRow | null; incoming: SessionRow; expected: SessionRow }[]
}

describe('общая таблица истины: last-write-wins', () => {
  it.each(cases.lww)('$name', (tc) => {
    expect(newer(tc.ts, tc.device, tc.cur_ts, tc.cur_device)).toBe(tc.newer)
  })
})

describe('общая таблица истины: подходы', () => {
  it.each(cases.sets)('$name', (tc) => {
    expect(mergeSet(tc.current ?? undefined, tc.incoming)).toEqual(tc.expected)
  })

  it.each(cases.sets.filter((c) => c.current !== null))('$name — не зависит от порядка', (tc) => {
    const forward = mergeSet(tc.current!, tc.incoming)
    const backward = mergeSet(tc.incoming, tc.current!)
    expect(forward).toEqual(backward)
  })
})

describe('общая таблица истины: тренировки', () => {
  it.each(cases.sessions)('$name', (tc) => {
    expect(mergeSession(tc.current ?? undefined, tc.incoming)).toEqual(tc.expected)
  })

  it.each(cases.sessions.filter((c) => c.current !== null))('$name — не зависит от порядка', (tc) => {
    const forward = mergeSession(tc.current!, tc.incoming)
    const backward = mergeSession(tc.incoming, tc.current!)
    expect(forward).toEqual(backward)
  })
})

describe('монотонность', () => {
  const base: SessionRow = {
    id: 's1',
    date: '2026-07-28',
    day_id: 'd1',
    program_hash: 'a',
    started_at: 100,
    finished_at: null,
    deleted: false,
    note: '',
    updated_ts: 100,
    updated_by: 'phone',
    rev: 1,
  }

  it('удалённая тренировка не воскресает поздней записью', () => {
    const deleted = { ...base, deleted: true, updated_ts: 100, updated_by: 'phone' }
    const later = { ...base, deleted: false, updated_ts: 999, updated_by: 'zzz' }
    expect(mergeSession(deleted, later).deleted).toBe(true)
  })

  it('завершённая тренировка не открывается заново', () => {
    const finished = { ...base, finished_at: 600 }
    const reopened = { ...base, finished_at: null, updated_ts: 999, updated_by: 'zzz' }
    expect(mergeSession(finished, reopened).finished_at).toBe(600)
  })
})
