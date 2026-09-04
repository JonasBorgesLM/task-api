import type { ReactNode } from 'react'
import { useState } from 'react'
import { Button } from './Button'
import styles from './AppShell.module.css'
import { DevicesIcon, SignOutIcon, UserIcon } from './icons'
import { Mark } from './Mark'
import type { MenuItem } from './Menu'
import { Menu } from './Menu'
import { Modal } from './Modal'
import { PageContainer } from './PageContainer'
import { ThemeToggle } from './ThemeToggle'

export interface AppShellProps {
  userEmail: string | undefined
  onLogout: () => void
  onLogoutAll: () => void
  children: ReactNode
}

/**
 * Persistent header/nav for the authenticated part of the app — CI-4 of
 * docs/changes/frontend-redesign, reworked in the design-review pass
 * that followed the Fase 14 release.
 *
 * The header carries three things and no more: the product name, the
 * theme control, and one account menu. Session actions used to sit in
 * that row as two full-width text buttons ("Log out", "Sign out of all
 * devices"), which made the least-used controls on the screen the
 * widest ones; they're behind a single icon trigger now, the same shape
 * every comparable product uses. A static "Tasks" label used to follow
 * the product name — dropped, since this app has exactly one
 * authenticated destination and a section label that never changes is
 * chrome, not wayfinding.
 *
 * Signing out of *all* devices is confirmed, unlike a plain log out:
 * it revokes sessions this browser can't see and can't restore, which
 * is exactly the kind of consequence a user should get to read before
 * committing to it.
 */
export function AppShell({ userEmail, onLogout, onLogoutAll, children }: AppShellProps) {
  const [confirmingLogoutAll, setConfirmingLogoutAll] = useState(false)

  const accountItems: MenuItem[] = [
    { key: 'logout', label: 'Log out', icon: <SignOutIcon />, onSelect: onLogout },
    {
      key: 'logout-all',
      label: 'Sign out of all devices',
      icon: <DevicesIcon />,
      onSelect: () => setConfirmingLogoutAll(true),
    },
  ]

  return (
    <div className={styles.shell}>
      <header className={styles.header}>
        <PageContainer>
          <div className={styles.headerRow}>
            {/* The product name carries the same ink the task titles do
                — the header was the one surface still reading as plain
                chrome while everything under it had a hand. The name
                stands on its own: a glyph beside it added a second mark
                competing with the ink for the same job. */}
            <nav aria-label="Main" className={styles.nav}>
              <span className={styles.appName}>
                <Mark>Task API</Mark>
              </span>
            </nav>
            {/* Email first, then the two glyphs as one pair — it used to
                sit between them, which read as a label belonging to
                whichever button you happened to look at. */}
            <div className={styles.userMenu}>
              <span className={styles.userEmail}>{userEmail}</span>
              <div className={styles.iconGroup}>
                <ThemeToggle />
                <Menu
                  triggerLabel={userEmail ? `Account: ${userEmail}` : 'Account'}
                  triggerIcon={<UserIcon />}
                  items={accountItems}
                  align="end"
                />
              </div>
            </div>
          </div>
        </PageContainer>
      </header>
      <main className={styles.main}>
        <PageContainer>{children}</PageContainer>
      </main>

      <Modal
        open={confirmingLogoutAll}
        onClose={() => setConfirmingLogoutAll(false)}
        title="Sign out of all devices?"
      >
        <p className={styles.confirmBody}>
          This ends every session on your account at once — this browser and every other device or
          browser you're currently signed in on. Anything signed in elsewhere is logged out
          immediately, and each will need your email and password again to get back in.
        </p>
        <p className={styles.confirmBody}>
          Your account and tasks are not affected; only active sessions are revoked. This is the
          right choice if you think someone else has access to a session, or you signed in on a
          device you no longer trust.
        </p>
        <div className={styles.confirmActions}>
          <Button variant="secondary" onClick={() => setConfirmingLogoutAll(false)}>
            Cancel
          </Button>
          <Button
            variant="danger"
            onClick={() => {
              setConfirmingLogoutAll(false)
              onLogoutAll()
            }}
          >
            Sign out everywhere
          </Button>
        </div>
      </Modal>
    </div>
  )
}
