import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiFetch, invalidateCsrfToken } from '../../api/client'
import { AuthProvider, useAuth } from './useAuth'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const USER = {
  id: 'u1',
  email: 'alice@example.com',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>
}

describe('useAuth', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    invalidateCsrfToken()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('hydrates as authenticated when GET /auth/me succeeds on boot', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ...USER, attachments_enabled: false }))

    const { result } = renderHook(() => useAuth(), { wrapper })

    expect(result.current.status).toBe('loading')
    await waitFor(() => expect(result.current.status).toBe('authenticated'))
    expect(result.current.user).toEqual(expect.objectContaining({ email: 'alice@example.com' }))
  })

  it('hydrates as unauthenticated when GET /auth/me returns 401 on boot', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { error: 'invalid or expired session token' }),
    )

    const { result } = renderHook(() => useAuth(), { wrapper })

    await waitFor(() => expect(result.current.status).toBe('unauthenticated'))
    expect(result.current.user).toBeNull()
  })

  it('stays in "loading" (never "unauthenticated") when GET /auth/me returns 503 on boot', async () => {
    // Found by CI-11's real docker-compose Postgres-outage E2E test, not
    // by inspection: a boot/reload during an outage cannot tell whether
    // the session is fine, so it must not guess "logged out" — that
    // would bounce an authenticated user to /login for a problem that
    // has nothing to do with their session. See client.ts's
    // finalize()/setUnauthorizedHandler, which makes the identical
    // 401-only distinction for every other endpoint.
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(
      jsonResponse(503, { error: 'service temporarily unavailable, please retry' }),
    )

    const { result } = renderHook(() => useAuth(), { wrapper })

    expect(result.current.status).toBe('loading')
    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(result.current.status).toBe('loading')
    expect(result.current.user).toBeNull()
  })

  it('login sets status to authenticated and hydrates the user via a fresh /me call', async () => {
    // LoginResponse.user is the bare User schema — it has no
    // attachments_enabled. login() re-hydrates via GET /auth/me
    // afterwards specifically so that field is known immediately,
    // rather than only after the next boot — see login()'s own doc
    // comment in useAuth.tsx for the real gap this closes.
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(401, {})) // boot /me — no session yet
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' })) // CSRF token fetch
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, { token: 'raw-token', expires_at: '2026-01-02T00:00:00Z', user: USER }),
    )
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ...USER, attachments_enabled: true })) // post-login /me

    const { result } = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(result.current.status).toBe('unauthenticated'))

    await act(async () => {
      await result.current.login('alice@example.com', 'correct horse battery staple')
    })

    expect(result.current.status).toBe('authenticated')
    expect(result.current.user).toEqual({ ...USER, attachments_enabled: true })
  })

  it('login throws the raw Response on failure, for the caller to classify', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(401, {}))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(jsonResponse(401, { error: 'invalid email or password' }))

    const { result } = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(result.current.status).toBe('unauthenticated'))

    await expect(result.current.login('alice@example.com', 'wrong')).rejects.toBeInstanceOf(
      Response,
    )
    // A failed login must not flip status to authenticated.
    expect(result.current.status).toBe('unauthenticated')
  })

  it('register does not change auth status — the backend never logs the caller in', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(401, {}))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(jsonResponse(201, USER))

    const { result } = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(result.current.status).toBe('unauthenticated'))

    await act(async () => {
      await result.current.register('alice@example.com', 'correct horse battery staple')
    })

    expect(result.current.status).toBe('unauthenticated')
    expect(result.current.user).toBeNull()
  })

  it('logout clears user/status regardless of the response', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ...USER, attachments_enabled: false }))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))

    const { result } = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(result.current.status).toBe('authenticated'))

    await act(async () => {
      await result.current.logout()
    })

    expect(result.current.status).toBe('unauthenticated')
    expect(result.current.user).toBeNull()
  })

  it('logoutAll clears user/status the same way', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ...USER, attachments_enabled: false }))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))

    const { result } = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(result.current.status).toBe('authenticated'))

    await act(async () => {
      await result.current.logoutAll()
    })

    expect(result.current.status).toBe('unauthenticated')
  })

  it('a 401 from ANY request elsewhere in the app clears the session too', async () => {
    // Proves the client.ts <-> useAuth wiring, not just useAuth's own
    // methods: a completely unrelated apiFetch call (e.g. one CI-7's
    // task list will make) that comes back 401 must clear the session
    // here too, via client.ts's setUnauthorizedHandler.
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ...USER, attachments_enabled: false }))
    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { error: 'invalid or expired session token' }),
    )

    const { result } = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(result.current.status).toBe('authenticated'))

    await act(async () => {
      await apiFetch('/v1/tasks')
    })

    expect(result.current.status).toBe('unauthenticated')
    expect(result.current.user).toBeNull()
  })
})
