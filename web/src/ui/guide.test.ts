import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { youtubeEmbedURL } from './youtube'

/**
 * The same truth table the Go tests read (internal/guide/guide_test.go).
 *
 * The id rule lives once in Go and once here, and the value ends up in the src of an iframe.
 * Code cannot be shared between the two, but the truth can — one file for both sides means a
 * one-sided edit fails on the other side, the way testdata/merge_cases.json works for the
 * merge rules.
 */
const ids = JSON.parse(
  readFileSync(new URL('../../../testdata/youtube_ids.json', import.meta.url), 'utf8'),
) as { accepted: string[]; rejected: string[] }

describe('youtubeEmbedURL', () => {
  it('builds the embed URL on the allowed domain', () => {
    const url = youtubeEmbedURL('7Yg2YVNdd8c')
    expect(url).not.toBeNull()
    expect(new URL(url as string).origin).toBe('https://www.youtube-nocookie.com')
    expect(new URL(url as string).pathname).toBe('/embed/7Yg2YVNdd8c')
  })

  it('keeps playback inline: full screen would throw the user out of the workout', () => {
    const params = new URL(youtubeEmbedURL('7Yg2YVNdd8c') as string).searchParams
    expect(params.get('playsinline')).toBe('1')
    expect(params.get('autoplay')).toBe('1')
  })

  it('passes a start offset through and omits it otherwise', () => {
    const withStart = new URL(youtubeEmbedURL('7Yg2YVNdd8c', 42) as string).searchParams
    expect(withStart.get('start')).toBe('42')

    const without = new URL(youtubeEmbedURL('7Yg2YVNdd8c') as string).searchParams
    expect(without.get('start')).toBeNull()

    const zero = new URL(youtubeEmbedURL('7Yg2YVNdd8c', 0) as string).searchParams
    expect(zero.get('start')).toBeNull()
  })

  // The value ends up in the src of an iframe. Anything that is not exactly a video id has
  // to produce no URL at all, not a URL that points somewhere unexpected.
  it.each(ids.rejected)('refuses %j', (bad) => {
    expect(youtubeEmbedURL(bad)).toBeNull()
  })

  it.each(ids.accepted)('accepts %j', (good) => {
    const url = youtubeEmbedURL(good)
    expect(url).not.toBeNull()
    expect(new URL(url as string).pathname).toBe(`/embed/${good}`)
  })
})
