import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LoginPage } from './LoginPage'
import { useAuth } from './useAuth'

vi.mock('./useAuth', () => ({ useAuth: vi.fn() }))

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function mockLogin(login: (email: string, password: string) => Promise<void>) {
  vi.mocked(useAuth).mockReturnValue({
    status: 'unauthenticated',
    user: null,
    register: vi.fn(),
    login,
    logout: vi.fn(),
    logoutAll: vi.fn(),
  })
}

function renderPage(
  initialEntries: Array<string | { pathname: string; state?: unknown }> = ['/login'],
) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/" element={<p>Home page</p>} />
        <Route path="/tasks" element={<p>Tasks page</p>} />
      </Routes>
    </MemoryRouter>,
  )
}

async function submit(user: ReturnType<typeof userEvent.setup>, email: string, password: string) {
  await user.type(screen.getByLabelText(/Email/), email)
  await user.type(screen.getByLabelText(/Password/), password)
  await user.click(screen.getByRole('button', { name: 'Log in' }))
}

describe('LoginPage', () => {
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('rejects an empty submission client-side, without calling login', async () => {
    const login = vi.fn<(email: string, password: string) => Promise<void>>()
    mockLogin(login)
    const user = userEvent.setup()
    renderPage()

    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(await screen.findByText('Email is required')).toBeInTheDocument()
    expect(screen.getByText('Password is required')).toBeInTheDocument()
    expect(login).not.toHaveBeenCalled()
  })

  it('logs in and navigates to / by default', async () => {
    const login = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockResolvedValue(undefined)
    mockLogin(login)
    const user = userEvent.setup()
    renderPage()

    await submit(user, 'alice@example.com', 'correct horse battery staple')

    expect(login).toHaveBeenCalledWith('alice@example.com', 'correct horse battery staple')
    expect(await screen.findByText('Home page')).toBeInTheDocument()
  })

  it('navigates back to the originally requested page after RequireAuth redirected here', async () => {
    const login = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockResolvedValue(undefined)
    mockLogin(login)
    const user = userEvent.setup()
    renderPage([{ pathname: '/login', state: { from: { pathname: '/tasks' } } }])

    await submit(user, 'alice@example.com', 'correct horse battery staple')

    expect(await screen.findByText('Tasks page')).toBeInTheDocument()
  })

  it('shows a success banner after arriving from a fresh registration', () => {
    const login = vi.fn<(email: string, password: string) => Promise<void>>()
    mockLogin(login)
    renderPage([{ pathname: '/login', state: { justRegistered: true } }])

    expect(screen.getByRole('status')).toHaveTextContent('Account created')
  })

  it('shows the SAME message for an unknown email and for a wrong password — anti-enumeration', async () => {
    const unknownEmailLogin = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockRejectedValue(jsonResponse(401, { error: 'invalid or expired session token' }))
    mockLogin(unknownEmailLogin)
    const user1 = userEvent.setup()
    const { unmount } = renderPage()
    await submit(user1, 'unknown@example.com', 'whatever')
    const messageForUnknownEmail = (await screen.findByRole('alert')).textContent
    unmount()

    const wrongPasswordLogin = vi
      .fn<(email: string, password: string) => Promise<void>>()
      // Same status, same generic body — this IS the point: classifyError
      // gives the frontend no signal to differentiate the two cases even
      // if it wanted to.
      .mockRejectedValue(jsonResponse(401, { error: 'invalid or expired session token' }))
    mockLogin(wrongPasswordLogin)
    const user2 = userEvent.setup()
    renderPage()
    await submit(user2, 'alice@example.com', 'wrong-password')
    const messageForWrongPassword = (await screen.findByRole('alert')).textContent

    expect(messageForUnknownEmail).toBe(messageForWrongPassword)
    expect(messageForUnknownEmail).toBe('Invalid email or password.')
  })

  it('shows a distinct message on 429 — must not look like a credential error', async () => {
    const login = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockRejectedValue(new Response('Too Many Requests', { status: 429 }))
    mockLogin(login)
    const user = userEvent.setup()
    renderPage()

    await submit(user, 'alice@example.com', 'correct horse battery staple')

    const message = (await screen.findByRole('alert')).textContent
    expect(message).not.toBe('Invalid email or password.')
    expect(message).toMatch(/too many login attempts/i)
  })
})
