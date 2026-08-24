import type { ProgressPoint } from '../state/selectors'
import { fmtDate, fmtWeight } from './format'
import './chart.css'

/**
 * A sparkline of best working weight by date.
 *
 * One series, so no legend is needed — the caption plays that role. The record point is
 * distinguished by more than colour: it has a larger radius and a surface-coloured ring, so
 * it stays legible under colour blindness and in print. That matters because the steel-blue
 * accent is deliberately muted and would separate poorly from the amber on saturation alone.
 *
 * There is deliberately no hover: this screen is opened from a phone, where hover does not
 * exist, and the dates at the edges and the caption with the current best do the labelling.
 */

const W = 320
const H = 64
const PAD_X = 6
const PAD_Y = 10

export function Sparkline({
  points,
  lowerIsBetter = false,
}: {
  points: ProgressPoint[]
  lowerIsBetter?: boolean
}) {
  if (points.length < 2) return null

  const weights = points.map((p) => p.weight)
  const min = Math.min(...weights)
  const max = Math.max(...weights)
  // A flat series must not collapse into a division by zero — it is drawn as a centre line.
  const span = max - min || 1

  // The best result: the smallest number when it counts machine assistance.
  const record = lowerIsBetter ? min : max

  const x = (i: number) => PAD_X + (i * (W - PAD_X * 2)) / (points.length - 1)
  // Better is always upwards, whichever way the numbers run. A line that climbs as the
  // assistance drops reads as progress, which is exactly what it is; drawn literally it
  // would read as decline. Nothing contradicts the inversion, because the axis carries no
  // value labels — the caption states the best result outright.
  const y = (weight: number) => {
    if (max === min) return H / 2
    const share = (weight - min) / span
    return H - PAD_Y - (lowerIsBetter ? 1 - share : share) * (H - PAD_Y * 2)
  }

  const path = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)} ${y(p.weight).toFixed(1)}`).join(' ')

  // The record is the first time the best value was reached: highlighting the last of the
  // equal points would mean declaring a repeat of an old result a record.
  const recordIndex = weights.indexOf(record)
  const lastIndex = points.length - 1

  const first = points[0]!
  const last = points[lastIndex]!

  return (
    <figure class="chart">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        class="chart-svg"
        role="img"
        aria-label={`Лучший рабочий вес: с ${fmtWeight(first.weight)} кг (${fmtDate(first.at)}) до ${fmtWeight(last.weight)} кг (${fmtDate(last.at)}), лучший результат ${fmtWeight(record)} кг`}
      >
        {/* The axis is deliberately faint: it is a reference, not content. */}
        <line x1="0" y1={H - 1} x2={W} y2={H - 1} class="chart-axis" />

        <path d={path} class="chart-line" fill="none" />

        {points.map((p, i) =>
          i === recordIndex ? null : (
            <circle key={p.sessionID} cx={x(i)} cy={y(p.weight)} r="2.5" class="chart-dot" />
          ),
        )}

        {/* A surface-coloured ring separates the record point from the line when it sits on it. */}
        <circle
          cx={x(recordIndex)}
          cy={y(record)}
          r="5.5"
          class="chart-record-ring"
        />
        <circle cx={x(recordIndex)} cy={y(record)} r="4" class="chart-record" />
      </svg>

      <figcaption class="chart-dates">
        <span>{fmtDate(first.at)}</span>
        <span>{fmtDate(last.at)}</span>
      </figcaption>
    </figure>
  )
}
