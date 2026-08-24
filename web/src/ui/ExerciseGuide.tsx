import { useState } from 'preact/hooks'
import { guideFor } from '../state/store'
import { mediaUrl } from '../sync/client'
import type { ExerciseMedia } from '../types'
import './guide.css'

/**
 * The technique reference for one exercise, expanded inside its card.
 *
 * Everything here is first-party. The text comes from IndexedDB and the demonstration from
 * this origin through the service worker's cache, so an opened guide makes no request to
 * anyone — which is what CONTEXT.md §9 asks for, and what the embedded YouTube player this
 * replaced could not give.
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

      {guide.media && <Demonstration exerciseID={exerciseID} media={guide.media} />}
    </div>
  )
}

/**
 * The demonstration: a silent looping clip, or the two end positions of the movement.
 *
 * Openly licensed video of gym exercises barely exists, so most exercises get frames. Both
 * kinds sit in the same box and carry the same caption, because from the reader's side they
 * answer the same question.
 *
 * Loading it is not gated behind a tap the way the YouTube frame was: there is no third party
 * left to withhold a request from, and a demonstration that needs a tap is a demonstration
 * half the time nobody sees.
 */
function Demonstration({ exerciseID, media }: { exerciseID: string; media: ExerciseMedia }) {
  const [failed, setFailed] = useState(false)

  return (
    <div class="guide-media">
      {failed ? (
        // Reached when the file is neither cached nor reachable — a guide opened for the
        // first time at the gym with no signal. The text above it is the part that matters
        // and is already on screen, so this stays a quiet line rather than an error.
        <div class="guide-media-missing">Показ не загрузился — нет сети</div>
      ) : media.kind === 'clip' ? (
        <video
          class="guide-media-box"
          src={mediaUrl(`${exerciseID}.mp4`)}
          /* Muted and inline are not preferences: iOS refuses to autoplay anything with
             sound, and without playsinline it takes the video full screen and throws the
             user out of the workout. */
          autoPlay
          loop
          muted
          playsInline
          preload="metadata"
          onError={() => setFailed(true)}
        />
      ) : (
        <Frames exerciseID={exerciseID} onError={() => setFailed(true)} />
      )}

      <div class="guide-media-caption">
        <a href={media.source} target="_blank" rel="noreferrer noopener">
          {media.credit}
        </a>{' '}
        · {media.license}
      </div>
    </div>
  )
}

/**
 * The two end positions, crossfading.
 *
 * Two stacked images with one of them animating its opacity: the movement between them is
 * left to the reader, which is honest — these are photographs, not frames of a film. The
 * animation is CSS, so it costs nothing and stops with the element.
 */
function Frames({ exerciseID, onError }: { exerciseID: string; onError: () => void }) {
  return (
    <div class="guide-media-box guide-frames">
      <img src={mediaUrl(`${exerciseID}-0.jpg`)} alt="Исходное положение" onError={onError} />
      <img
        class="guide-frame-end"
        src={mediaUrl(`${exerciseID}-1.jpg`)}
        alt="Конечное положение"
        onError={onError}
      />
    </div>
  )
}
