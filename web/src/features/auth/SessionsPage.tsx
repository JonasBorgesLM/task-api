import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import type { components } from '../../api/types'
import { AppShell } from '../../components/AppShell'
import { Button } from '../../components/Button'
import { RefreshIcon, TrashIcon } from '../../components/icons'
import { Skeleton } from '../../components/Skeleton'
import { Toast } from '../../components/Toast'
import styles from './SessionsPage.module.css'
import { useAuth } from './useAuth'

type Session = components['schemas']['SessionResponse']

type Status = 'loading' | 'empty' | 'error' | 'success'

const SKELETON_ROWS = 3

function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  })
}

/**
 * GET/DELETE /v1/auth/sessions (issue #224) — the gap between "this
 * session" (Log out) and "every session" (Sign out of all devices):
 * shows every active session with when it was created and when it
 * expires, and lets the caller revoke exactly one.
 *
 * Revoking the session marked "This device" is not disabled here — the
 * server does not special-case it either (see
 * Service.RevokeSession's doc comment): it is exactly what "Log out"
 * already does under a different address. The next request this app
 * makes after that will 401 through the same useAuth flow that already
 * handles an expired or otherwise invalidated session, so nothing extra
 * needs to happen here for that case.
 *
 * Its own route/chunk (see App.tsx) — a settings-shaped detour reached
 * from the account menu, not primary navigation (see AppShell's own
 * doc comment).
 */
export function SessionsPage() {
  const { user, logout, logoutAll } = useAuth()
  const navigate = useNavigate()
  const [status, setStatus] = useState<Status>('loading')
  const [sessions, setSessions] = useState<Session[]>([])
  const [error, setError] = useState<ApiError | null>(null)
  const [revokingId, setRevokingId] = useState<string | null>(null)
  const [revokeError, setRevokeError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)

  const load = useCallback(async () => {
    setStatus('loading')
    const response = await apiFetch('/v1/auth/sessions')
    if (!response.ok) {
      setError(await classifyError(response))
      setStatus('error')
      return
    }
    const body = (await response.json()) as Session[]
    setSessions(body)
    setError(null)
    setStatus(body.length === 0 ? 'empty' : 'success')
  }, [])

  // oxlint's set-state-in-effect rule: fetch-on-mount is the canonical
  // "synchronize with an external system" case its own guidance carves
  // out — same accepted pattern as useTasks.tsx and AttachmentList.tsx.
  useEffect(() => {
    void load()
  }, [load])

  async function handleRevoke(id: string) {
    setRevokeError(null)
    setRevokingId(id)
    try {
      const response = await apiFetch(`/v1/auth/sessions/${id}`, { method: 'DELETE' })
      if (!response.ok) {
        const classified = await classifyError(response)
        setRevokeError(
          classified.kind === 'not_found'
            ? 'This session is already gone.'
            : 'Could not revoke this session. Please try again.',
        )
        return
      }
      const next = sessions.filter((s) => s.id !== id)
      setSessions(next)
      setStatus(next.length === 0 ? 'empty' : 'success')
      setSuccessMessage('Session revoked.')
    } finally {
      setRevokingId(null)
    }
  }

  return (
    <AppShell
      userEmail={user?.email}
      onLogout={() => void logout()}
      onLogoutAll={() => void logoutAll()}
      onManageSessions={() => navigate('/sessions')}
    >
      {successMessage && (
        <Toast
          message={successMessage}
          variant="success"
          onDismiss={() => setSuccessMessage(null)}
        />
      )}

      <div className={styles.header}>
        <h1 className={styles.title}>Active sessions</h1>
        <Button variant="secondary" onClick={() => navigate('/')}>
          Back to tasks
        </Button>
      </div>

      {status === 'loading' && (
        <ul className={styles.list} aria-busy="true" aria-label="Loading sessions">
          {Array.from({ length: SKELETON_ROWS }, (_, i) => (
            <li key={i} className={styles.skeletonItem}>
              <Skeleton width="60%" height="1rem" />
            </li>
          ))}
        </ul>
      )}

      {status === 'error' && (
        <p className={styles.error} role="alert">
          {error?.kind === 'unavailable'
            ? "Couldn't load sessions — the service is temporarily unavailable. "
            : "Couldn't load sessions. "}
          <Button variant="secondary" onClick={() => void load()}>
            <RefreshIcon />
            Retry
          </Button>
        </p>
      )}

      {status === 'empty' && <p className={styles.empty}>No active sessions.</p>}

      {status === 'success' && (
        <ul className={styles.list}>
          {sessions.map((session) => (
            <li key={session.id} className={styles.item}>
              <div className={styles.itemInfo}>
                <span className={styles.sessionLabel}>
                  {session.is_current ? 'This device' : 'Another session'}
                  {session.is_current && (
                    <span className={styles.currentBadge}>Current</span>
                  )}
                </span>
                <span className={styles.sessionMeta}>
                  Signed in {formatDateTime(session.created_at)} — expires{' '}
                  {formatDateTime(session.expires_at)}
                </span>
              </div>
              <Button
                variant="dangerQuiet"
                loading={revokingId === session.id}
                onClick={() => void handleRevoke(session.id)}
              >
                <TrashIcon />
                Revoke
              </Button>
            </li>
          ))}
        </ul>
      )}

      {revokeError && (
        <p className={styles.error} role="alert">
          {revokeError}
        </p>
      )}
    </AppShell>
  )
}
