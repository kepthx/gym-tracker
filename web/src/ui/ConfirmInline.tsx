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
  onOpenChange,
}: {
  label: string
  question: string
  confirmLabel: string
  danger?: boolean
  onConfirm: () => void
  /** Reports the open state: expanded, the question needs far more room than the button
      it replaced, and a cramped row may have to give up neighbours to make space. */
  onOpenChange?: (open: boolean) => void
}) {
  const [open, setOpen] = useState(false)

  function toggle(next: boolean) {
    setOpen(next)
    onOpenChange?.(next)
  }

  if (!open) {
    return (
      <button
        class={`btn btn-quiet ${danger ? 'confirm-danger' : ''}`}
        onClick={() => toggle(true)}
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
          toggle(false)
          onConfirm()
        }}
      >
        {confirmLabel}
      </button>
      <button class="btn btn-quiet" onClick={() => toggle(false)}>
        Отмена
      </button>
    </div>
  )
}
