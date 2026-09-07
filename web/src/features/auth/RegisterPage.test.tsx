import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RegisterPage } from './RegisterPage'
import { useAuth } from './useAuth'

vi.mock('./useAuth', () => ({ useAuth: vi.fn() }))

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function mockRegister(register: (email: string, password: string) => Promise<void>) {
  vi.mocked(useAuth).mockReturnValue({
    status: 'unauthenticated',
    user: null,
    register,
    login: vi.fn(),
    logout: vi.fn(),
    logoutAll: vi.fn(),
  })
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/register']}>
      <Routes>
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/login" element={<p>Login page</p>} />
      </Routes>
    </MemoryRouter>,
  )
}

// Fills email/password/confirm-password and submits. Password and
// confirmPassword default to the same value — most tests care about
// something other than the confirmation match, so this keeps them from
// having to spell out an identical second value every time. Tests that
// specifically exercise the mismatch path pass distinct values.
interface FillAndSubmitOptions {
  email?: string
  password?: string
  confirmPassword?: string
}

async function fillAndSubmit(
  user: ReturnType<typeof userEvent.setup>,
  { email = 'alice@example.com', password = 'correct horse battery staple', confirmPassword }: FillAndSubmitOptions,
) {
  confirmPassword ??= password
  await user.type(screen.getByLabelText(/^Email/), email)
  // { selector: 'input' } restricts the match to form controls: without
  // it, /^Password/ also matches the live requirements checklist's own
  // aria-label="Password requirements" (a <ul>, not an input).
  await user.type(screen.getByLabelText(/^Password/, { selector: 'input' }), password)
  await user.type(
    screen.getByLabelText(/^Confirm password/, { selector: 'input' }),
    confirmPassword,
  )
  await user.click(screen.getByRole('button', { name: 'Create account' }))
}

describe('RegisterPage', () => {
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('rejects an empty submission client-side, without calling register', async () => {
    const register = vi.fn<(email: string, password: string) => Promise<void>>()
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByText('Email is required')).toBeInTheDocument()
    expect(screen.getByText('Must be at least 8 characters')).toBeInTheDocument()
    expect(register).not.toHaveBeenCalled()
  })

  it('rejects an invalid email client-side', async () => {
    const register = vi.fn<(email: string, password: string) => Promise<void>>()
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await fillAndSubmit(user, { email: 'not-an-email' })

    expect(await screen.findByText('Enter a valid email address')).toBeInTheDocument()
    expect(register).not.toHaveBeenCalled()
  })

  it('rejects a confirmation that does not match the password, without calling register', async () => {
    const register = vi.fn<(email: string, password: string) => Promise<void>>()
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await fillAndSubmit(user, {
      password: 'correct horse battery staple',
      confirmPassword: 'a different phrase entirely',
    })

    expect(await screen.findByText('Passwords do not match')).toBeInTheDocument()
    expect(register).not.toHaveBeenCalled()
  })

  it('shows a live checklist that mirrors the server rule as the password is typed', async () => {
    const register = vi.fn<(email: string, password: string) => Promise<void>>()
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    const list = screen.getByRole('list', { name: 'Password requirements' })
    expect(list).toBeInTheDocument()

    // A weak, commonly-used password that still satisfies plain length
    // (the client-side gate) does NOT flip the checklist's "not a
    // commonly used password" item to met — the checklist is not fooled
    // by a submission that would still get a 400 from the server.
    await user.type(screen.getByLabelText(/^Password/, { selector: 'input' }), 'welcome1')
    expect(list).toHaveTextContent('Not a commonly used password')
  })

  it('does not gate submission on the not-common/not-predictable checklist — the server decides', async () => {
    // A password satisfying the client-side schema (length 8-72) but
    // failing the checklist (a common password) must still reach
    // register(): PasswordRequirements is documented as informational
    // only, never a client-side gate.
    const register = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockResolvedValue(undefined)
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await fillAndSubmit(user, { password: 'welcome1' })

    expect(register).toHaveBeenCalledWith('alice@example.com', 'welcome1')
  })

  it('registers, then navigates to /login without authenticating', async () => {
    const register = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockResolvedValue(undefined)
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await fillAndSubmit(user, {})

    expect(register).toHaveBeenCalledWith('alice@example.com', 'correct horse battery staple')
    expect(await screen.findByText('Login page')).toBeInTheDocument()
  })

  it("shows the server message verbatim on a 409 (email already registered)", async () => {
    const register = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockRejectedValue(jsonResponse(409, { error: 'email already registered' }))
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await fillAndSubmit(user, {})

    expect(await screen.findByRole('alert')).toHaveTextContent('email already registered')
    // Must NOT have navigated away on failure.
    expect(screen.queryByText('Login page')).not.toBeInTheDocument()
  })

  it('shows a distinct message on 429, not the generic fallback', async () => {
    const register = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockRejectedValue(new Response('Too Many Requests', { status: 429 }))
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await fillAndSubmit(user, {})

    expect(await screen.findByRole('alert')).toHaveTextContent(/too many attempts/i)
  })
})
