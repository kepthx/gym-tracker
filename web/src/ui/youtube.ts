/**
 * The embed URL for a technique video.
 *
 * The guides file supplies an id, never a URL, and this is the only place a URL is built
 * from it. The check is deliberately repeated here even though the server already validated
 * the id on load: the result goes into the src of an iframe, and a value that reaches an
 * iframe unchecked is the kind of thing that is discovered later rather than sooner.
 *
 * youtube-nocookie is the domain the CSP allows, and the only one. No YouTube JS API, no
 * thumbnail from ytimg — a bare frame, created only after an explicit tap on play.
 */
const ID_RE = /^[A-Za-z0-9_-]{11}$/

export function youtubeEmbedURL(id: string, startSec?: number): string | null {
  if (!ID_RE.test(id)) return null

  const params = new URLSearchParams({
    rel: '0',
    modestbranding: '1',
    // Without this iOS takes the video full-screen the moment it starts, which throws the
    // user out of the workout screen.
    playsinline: '1',
    // The frame appears only after a tap on play, so that tap is what starts playback.
    autoplay: '1',
  })
  if (startSec !== undefined && startSec > 0) params.set('start', String(Math.floor(startSec)))

  return `https://www.youtube-nocookie.com/embed/${id}?${params}`
}
