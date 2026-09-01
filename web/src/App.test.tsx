import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { invalidateCsrfToken } from './api/client'
import { AuthProvider } from './features/auth/useAuth'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function renderApp(initialPath: string) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <AuthProvider>
        <App />
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('App routing', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    invalidateCsrfToken()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('redirects an unauthenticated visitor at / to /login', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { error: 'invalid or expired session token' }),
    )

    renderApp('/')

    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument()
  })

  it('shows the authenticated home for an already-authenticated visitor at /', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        id: 'u1',
        email: 'alice@example.com',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        attachments_enabled: false,
      }),
    )
    // TaskList (CI-7) mounts alongside the account info and fetches its
    // own first page.
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))

    renderApp('/')

    expect(await screen.findByText('Logged in as alice@example.com')).toBeInTheDocument()
    expect(await screen.findByText("You don't have any tasks yet.")).toBeInTheDocument()
  })

  it('the full flow: register → login → /me hydrates → logout redirects back to /login', async () => {
    const fetchMock = vi.mocked(fetch)
    const user = userEvent.setup()

    // Boot: no session yet.
    fetchMock.mockResolvedValueOnce(jsonResponse(401, {}))
    renderApp('/register')
    await screen.findByRole('heading', { name: 'Create an account' })

    // Register — first mutating call of the session, so it also fetches
    // a CSRF token.
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: 'u1',
        email: 'alice@example.com',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      }),
    )
    await user.type(screen.getByLabelText(/Email/), 'alice@example.com')
    await user.type(screen.getByLabelText(/Password/), 'correct horse battery staple')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    await screen.findByRole('heading', { name: 'Log in' })
    expect(screen.getByText('Account created — log in below.')).toBeInTheDocument()

    // Login — CSRF token is already cached from registration, so no
    // second GET /auth/csrf-token here.
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        token: 'raw-token',
        expires_at: '2026-01-02T00:00:00Z',
        user: {
          id: 'u1',
          email: 'alice@example.com',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        },
      }),
    )
    await user.type(screen.getByLabelText(/Email/), 'alice@example.com')
    await user.type(screen.getByLabelText(/Password/), 'correct horse battery staple')
    // TaskList (CI-7) mounts alongside the account info once login
    // succeeds and fetches its own first page.
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(await screen.findByText('Logged in as alice@example.com')).toBeInTheDocument()
    expect(await screen.findByText("You don't have any tasks yet.")).toBeInTheDocument()

    // Logout — clears state, and RequireAuth (still mounted at /)
    // reacts to the status change on its own, with no explicit
    // navigate() call anywhere in the logout path.
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await user.click(screen.getByRole('button', { name: 'Log out' }))

    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument()
  })
})
