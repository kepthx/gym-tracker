import { useEffect, useState } from 'preact/hooks'
import { guideFor } from '../state/store'
import { youtubeEmbedURL } from './youtube'
import './guide.css'

/**
 * The technique reference for one exercise, expanded inside its card.
 *
 * The text comes from IndexedDB, so it reads in a basement gym with no signal. The video
 * does not, and says so rather than showing a frame that will never load.
 */
export function ExerciseGuide({ exerciseID }: { exerciseID: string }) {
  const guide = guideFor(exerciseID)

  if (!guide) {
    return <div class="guide guide-missing">Справки по этому упражнению нет.</div>
  }

  return (
    <div class="guide">
      <p class="guide-summary">{guide.summary}</p>

      <div class="guide-label">Техника</div>
      <ul class="guide-list">
        {guide.cues.map((cue) => (
          <li key={cue}>{cue}</li>
        ))}
      </ul>

      {guide.mistakes && guide.mistakes.length > 0 && (
        <>
          <div class="guide-label">Ошибки</div>
          <ul class="guide-list guide-mistakes">
            {guide.mistakes.map((mistake) => (
              <li key={mistake}>{mistake}</li>
            ))}
          </ul>
        </>
      )}

      {guide.video && (
        <VideoFrame
          youtubeID={guide.video.youtube_id}
          startSec={guide.video.start_sec}
          title={guide.video.title}
          author={guide.video.author}
        />
      )}
    </div>
  )
}

/**
 * The video, and the one place in the application that reaches a third party.
 *
 * Until play is tapped there is no iframe: the box is drawn in CSS, the poster is not pulled
 * from ytimg, and no YouTube script is loaded. So an open guide — and the workout screen it
 * sits on — sends Google nothing at all. That is what keeps CONTEXT.md §9 honest with a
 * player on the screen, and why the frame must stay created on demand rather than hidden.
 */
function VideoFrame({
  youtubeID,
  startSec,
  title,
  author,
}: {
  youtubeID: string
  startSec?: number
  title: string
  author: string
}) {
  const [playing, setPlaying] = useState(false)
  const online = useOnline()

  // Whether the id is usable and whether play was tapped are two different facts, so they
  // are two different values. Folding them into one nullable src made a guide with an
  // unusable id render a play button that did nothing at all, forever, with no message.
  const src = youtubeEmbedURL(youtubeID, startSec)

  // The author is validated on the server, but a guide cached before that check existed can
  // still be sitting in IndexedDB, and "Присед · " reads as a truncated string.
  const caption = <div class="guide-video-caption">{author ? `${title} · ${author}` : title}</div>

  let box
  if (src === null) {
    box = <div class="guide-video-offline">Видео к этому упражнению не открывается</div>
  } else if (playing) {
    // Once the frame is up it stays up, online or not. navigator.onLine flaps on iOS, and
    // tearing the player down on a blip restarts the video from the beginning — and asks
    // YouTube for it again with no tap behind it, which is the one thing this screen
    // promises not to do.
    box = (
      <iframe
        class="guide-video-box"
        src={src}
        title={title}
        allow="autoplay; encrypted-media; picture-in-picture; fullscreen"
        // REQUIRED, and the tightest value that works. The embedded player identifies the
        // embedding site by the Referer header and refuses to play without one — "Ошибка
        // 153. Ошибка настройки видеопроигрывателя", which is what no-referrer produced
        // here. This attribute overrides the document's same-origin policy for this one
        // request, and cross-origin it sends the bare origin: no path, no query, nothing
        // about which exercise was opened or who opened it. CONTEXT.md §9 still holds.
        referrerpolicy="strict-origin-when-cross-origin"
        loading="lazy"
      />
    )
  } else if (!online) {
    // A flat strip, not an empty 16:9 box: there is nothing to show, and pretending there
    // is would just eat the screen.
    box = <div class="guide-video-offline">Без сети видео не откроется</div>
  } else {
    box = (
      <button
        class="guide-video-box guide-video-facade"
        onClick={() => setPlaying(true)}
        aria-label={`Смотреть: ${title}`}
      >
        <span class="guide-play" aria-hidden="true" />
        <span class="guide-video-hint">youtube.com</span>
      </button>
    )
  }

  return (
    <div class="guide-video">
      {box}
      {caption}
    </div>
  )
}

/** Connectivity, as a rendered value: the guide is opened at the gym, where it changes. */
function useOnline(): boolean {
  const [online, setOnline] = useState(() => navigator.onLine)

  useEffect(() => {
    const update = () => setOnline(navigator.onLine)
    addEventListener('online', update)
    addEventListener('offline', update)
    return () => {
      removeEventListener('online', update)
      removeEventListener('offline', update)
    }
  }, [])

  return online
}
