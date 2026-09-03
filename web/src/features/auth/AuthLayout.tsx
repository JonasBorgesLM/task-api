import type { ReactNode } from 'react'
import styles from './AuthLayout.module.css'

export interface AuthLayoutProps {
  title: string
  children: ReactNode
}

// Genuine, verifiable claims about this app's own backend — not
// marketing fluff. "Enforced on the server" / "before a single byte is
// stored" / "CSRF-protected" are all real, testable properties (see
// CLAUDE.md's "Task status: transition rules live in Service" and
// "Quota de anexos" in docs/DECISIONS.md), chosen deliberately over
// generic SaaS copy.
const HIGHLIGHTS = [
  {
    title: 'Full task lifecycle',
    description:
      'Create, edit, and move tasks through pending, in progress, done, and cancelled — with legal transitions enforced on the server, not just the UI.',
  },
  {
    title: 'File attachments',
    description:
      'Upload, preview, and download files on any task, with per-file and per-account storage limits enforced before a single byte is stored.',
  },
  {
    title: 'Secure by design',
    description:
      'Sessions live in an httpOnly cookie, every write is CSRF-protected, and every task is scoped to its owner — no cross-account access, ever.',
  },
]

/**
 * Split-screen shell for LoginPage/RegisterPage — CI-5 of
 * docs/changes/frontend-redesign, expanded from "just a card" after a
 * request for a login screen with a real marketing panel describing the
 * product. Only this app's own tokens are used (single accent, no
 * gradient, no decorative shadow) — not the colors/effects of the
 * external template that motivated the request; see
 * docs/changes/frontend-redesign/validation.md's AM-5 for why.
 *
 * The marketing column is genuine product information, not filler, so
 * it stays in the accessibility tree rather than aria-hidden — it comes
 * *after* the form in DOM order instead, which is what actually keeps it
 * out of the way of the real task here (logging in): tab order and a
 * screen reader's linear reading both reach the form first regardless of
 * the two-column layout on wide screens.
 */
export function AuthLayout({ title, children }: AuthLayoutProps) {
  return (
    <div className={styles.page}>
      <div className={styles.formColumn}>
        <div className={styles.formColumnInner}>
          <span className={styles.appName}>Task API</span>
          <div className={styles.card}>
            <h1 className={styles.title}>{title}</h1>
            {children}
          </div>
        </div>
      </div>
      <aside className={styles.marketing} aria-label="About Task API">
        <div className={styles.marketingInner}>
          <h2 className={styles.marketingHeadline}>Task management, done right.</h2>
          <p className={styles.marketingSubhead}>
            A fast, secure task tracker built for teams that need real ownership and real security —
            not just another to-do list.
          </p>
          <ul className={styles.highlights}>
            {HIGHLIGHTS.map((item) => (
              <li key={item.title} className={styles.highlight}>
                <span className={styles.highlightDot} aria-hidden="true" />
                <div>
                  <p className={styles.highlightTitle}>{item.title}</p>
                  <p className={styles.highlightDescription}>{item.description}</p>
                </div>
              </li>
            ))}
          </ul>
        </div>
      </aside>
    </div>
  )
}
