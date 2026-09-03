import type { ReactNode } from 'react'
import { Button } from './Button'
import styles from './AppShell.module.css'
import { PageContainer } from './PageContainer'

export interface AppShellProps {
  userEmail: string | undefined
  onLogout: () => void
  onLogoutAll: () => void
  children: ReactNode
}

/**
 * Persistent header/nav for the authenticated part of the app — CI-4 of
 * docs/changes/frontend-redesign, the highest-impact finding from the
 * visual audit that started this phase. Before this, AuthenticatedHome
 * (App.tsx) was a bare paragraph ("Logged in as ...") plus two logout
 * buttons plus the list — no name, no landmarks, nothing that read as a
 * product rather than a test page.
 *
 * Deliberately v1-minimal: no global search, no avatar, no theme
 * switcher — see docs/changes/frontend-redesign/validation.md's AM-4 for
 * why those wait for a concrete need. "Tasks" in the nav is a static
 * current-section label, not a link — this app has exactly one
 * authenticated destination today, and a nav item that goes nowhere else
 * would be a fake affordance, not real wayfinding.
 */
export function AppShell({ userEmail, onLogout, onLogoutAll, children }: AppShellProps) {
  return (
    <div className={styles.shell}>
      <header className={styles.header}>
        <PageContainer>
          <div className={styles.headerRow}>
            <nav aria-label="Main" className={styles.nav}>
              <span className={styles.appName}>Task API</span>
              <span className={styles.currentSection} aria-current="page">
                Tasks
              </span>
            </nav>
            <div className={styles.userMenu}>
              <span className={styles.userEmail}>{userEmail}</span>
              <Button variant="secondary" onClick={onLogout}>
                Log out
              </Button>
              <Button variant="secondary" onClick={onLogoutAll}>
                Sign out of all devices
              </Button>
            </div>
          </div>
        </PageContainer>
      </header>
      <main className={styles.main}>
        <PageContainer>{children}</PageContainer>
      </main>
    </div>
  )
}
