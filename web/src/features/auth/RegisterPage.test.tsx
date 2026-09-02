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

    await user.type(screen.getByLabelText(/Email/), 'not-an-email')
    await user.type(screen.getByLabelText(/Password/), 'correct horse battery staple')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByText('Enter a valid email address')).toBeInTheDocument()
    expect(register).not.toHaveBeenCalled()
  })

  it('registers, then navigates to /login without authenticating', async () => {
    const register = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockResolvedValue(undefined)
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await user.type(screen.getByLabelText(/Email/), 'alice@example.com')
    await user.type(screen.getByLabelText(/Password/), 'correct horse battery staple')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(register).toHaveBeenCalledWith('alice@example.com', 'correct horse battery staple')
    expect(await screen.findByText('Login page')).toBeInTheDocument()
  })

  it('shows the server message verbatim on a 409 (email already registered)', async () => {
    const register = vi
      .fn<(email: string, password: string) => Promise<void>>()
      .mockRejectedValue(jsonResponse(409, { error: 'email already registered' }))
    mockRegister(register)
    const user = userEvent.setup()
    renderPage()

    await user.type(screen.getByLabelText(/Email/), 'alice@example.com')
    await user.type(screen.getByLabelText(/Password/), 'correct horse battery staple')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

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

    await user.type(screen.getByLabelText(/Email/), 'alice@example.com')
    await user.type(screen.getByLabelText(/Password/), 'correct horse battery staple')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/too many attempts/i)
  })
})
