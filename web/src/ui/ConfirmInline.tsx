import { useState } from 'preact/hooks'
import './confirm.css'

/**
 * Confirmation of an action.
 *
 * Not a system dialog: alert and confirm simply do not work in standalone mode on iOS.
 * And not a modal: the UI does without those on principle. The button expands in place,
 * inside the same card, with no dimming and no overlay.
 */
export function ConfirmInline({
  label,
  question,
  confirmLabel,
  danger,
  onConfirm,
}: {
  label: string
  question: string
  confirmLabel: string
  danger?: boolean
  onConfirm: () => void
}) {
  const [open, setOpen] = useState(false)

  if (!open) {
    return (
      <button
        class={`btn btn-quiet ${danger ? 'confirm-danger' : ''}`}
        onClick={() => setOpen(true)}
      >
        {label}
      </button>
    )
  }

  return (
    <div class="confirm">
      <span class="confirm-question">{question}</span>
      <button
        class={`btn btn-quiet ${danger ? 'confirm-danger' : ''}`}
        onClick={() => {
          setOpen(false)
          onConfirm()
        }}
      >
        {confirmLabel}
      </button>
      <button class="btn btn-quiet" onClick={() => setOpen(false)}>
        Отмена
      </button>
    </div>
  )
}
