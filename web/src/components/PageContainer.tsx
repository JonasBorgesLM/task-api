import type { ReactNode } from 'react'
import styles from './PageContainer.module.css'

export interface PageContainerProps {
  children: ReactNode
}

/**
 * Centers content at a deliberate max width (`--content-max-width`) —
 * CI-2 of docs/changes/frontend-redesign. Every screen (login, register,
 * the task list) shares this instead of each picking its own, or — as
 * happened before this change — inheriting an accidental one from the
 * leftover Vite scaffold's `#root` rule.
 *
 * Deliberately just width/centering: no card, no border, no background.
 * The app-shell (CI-4) and the auth card (CI-5) each add their own visual
 * treatment on top of this, but both need the same width primitive
 * underneath, which is the only thing this component owns.
 */
export function PageContainer({ children }: PageContainerProps) {
  return <div className={styles.container}>{children}</div>
}
