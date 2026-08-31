import { useEffect } from 'react'
import styles from './Toast.module.css'

export type ToastVariant = 'info' | 'success' | 'error'

export interface ToastProps {
  message: string
  variant?: ToastVariant
  /** Called when the toast should be removed — by its own timer or by the dismiss button. */
  onDismiss: () => void
  /** Auto-dismiss after this many ms. 0 disables auto-dismiss (error toasts default to this). */
  duration?: number
}

const DEFAULT_DURATION_MS = 5000

/**
 * role differs by variant on purpose: 'error' uses role="alert"
 * (assertive — interrupts immediately, matching how urgently a person
 * needs to know their action failed), 'info'/'success' use
 * role="status" (polite — announced without interrupting whatever the
 * screen reader is already reading).
 */
export function Toast({ message, variant = 'info', onDismiss, duration }: ToastProps) {
  const effectiveDuration = duration ?? (variant === 'error' ? 0 : DEFAULT_DURATION_MS)

  useEffect(() => {
    if (effectiveDuration <= 0) return
    const timer = setTimeout(onDismiss, effectiveDuration)
    return () => clearTimeout(timer)
  }, [effectiveDuration, onDismiss])

  return (
    <div
      className={[styles.toast, styles[variant]].join(' ')}
      role={variant === 'error' ? 'alert' : 'status'}
    >
      <span className={styles.message}>{message}</span>
      <button type="button" className={styles.dismiss} onClick={onDismiss} aria-label="Dismiss">
        ×
      </button>
    </div>
  )
}
