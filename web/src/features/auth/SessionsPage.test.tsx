/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateCsrfToken } from '../../api/client'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { SessionsPage } from './SessionsPage'
import { useAuth } from './useAuth'

vi.mock('./useAuth', () => ({ useAuth: vi.fn() }))

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const CSRF_RESPONSE = () => jsonResponse(200, { csrf_token: 'test-csrf-token' })

const CURRENT_SESSION = {
  id: 'session-current',
  created_at: '2026-01-01T00:00:00Z',
  expires_at: '2026-01-08T00:00:00Z',
  is_current: true,
}

const OTHER_SESSION = {
  id: 'session-other',
  created_at: '2026-01-02T00:00:00Z',
  expires_at: '2026-01-09T00:00:00Z',
  is_current: false,
}

function mockAuth() {
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: {
      id: 'u1',
      email: 'alice@example.com',
      created_at: '',
      updated_at: '',
      attachments_enabled: true,
    },
    register: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    logoutAll: vi.fn(),
  })
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/sessions']}>
      <SessionsPage />
    </MemoryRouter>,
  )
}

describe('SessionsPage.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'SessionsPage.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'SessionsPage.module.css')
  })
})

describe('SessionsPage', () => {
  beforeEach(() => {
    invalidateCsrfToken()
    mockAuth()
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('lists sessions, marking the current one', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [CURRENT_SESSION, OTHER_SESSION]))
    renderPage()

    expect(await screen.findByText('This device')).toBeInTheDocument()
    expect(screen.getByText('Current')).toBeInTheDocument()
    expect(screen.getByText('Another session')).toBeInTheDocument()
  })

  it('shows an empty state when there are no sessions', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    renderPage()

    expect(await screen.findByText('No active sessions.')).toBeInTheDocument()
  })

  it('shows an error state and can retry on load failure', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(new Response('', { status: 500 }))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [OTHER_SESSION]))
    const user = userEvent.setup()
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent(/couldn't load sessions/i)

    await user.click(screen.getByRole('button', { name: /retry/i }))

    expect(await screen.findByText('Another session')).toBeInTheDocument()
  })

  it('revokes a session and removes it from the list', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [CURRENT_SESSION, OTHER_SESSION]))
    fetchMock.mockResolvedValueOnce(CSRF_RESPONSE())
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('Another session')
    const otherItem = screen.getByText('Another session').closest('li')!
    await user.click(within(otherItem).getByRole('button', { name: /revoke/i }))

    expect(await screen.findByText('Session revoked.')).toBeInTheDocument()
    expect(screen.queryByText('Another session')).not.toBeInTheDocument()
    // The current session must survive revoking a *different* one.
    expect(screen.getByText('This device')).toBeInTheDocument()
  })

  it('shows a message when revoking an already-gone session', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [OTHER_SESSION]))
    fetchMock.mockResolvedValueOnce(CSRF_RESPONSE())
    fetchMock.mockResolvedValueOnce(jsonResponse(404, { error: 'user not found' }))
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('Another session')
    await user.click(screen.getByRole('button', { name: /revoke/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/already gone/i)
  })
})
