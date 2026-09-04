import type { ReactNode } from 'react'
import styles from './Mark.module.css'

export interface MarkProps {
  children: ReactNode
  className?: string
}

/**
 * A marker stroke behind text — the app's one piece of ink, used for
 * the product name and for every task title.
 *
 * An inline span rather than a background on the block: it hugs the
 * words instead of the line box, and box-decoration-break: clone gives
 * each wrapped line its own rounded stroke, which is what a round-nibbed
 * pen actually leaves behind. A single bar spanning a ragged right edge
 * is what a rectangle does, not what a pen does.
 *
 * Shared rather than copied into both callers because it is identity:
 * if the ink changes, it has to change in one place, or the header and
 * the cards stop looking like the same hand drew them.
 */
export function Mark({ children, className }: MarkProps) {
  return <span className={[styles.mark, className].filter(Boolean).join(' ')}>{children}</span>
}
