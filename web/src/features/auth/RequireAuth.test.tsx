import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RequireAuth } from './RequireAuth'
import { useAuth } from './useAuth'

vi.mock('./useAuth', () => ({ useAuth: vi.fn() }))

// RequireAuth's own job — "given a status, render the right thing" —
// is tested here in isolation from how that status gets computed,
// which useAuth.test.tsx already covers against a real AuthProvider.
function mockStatus(status: 'loading' | 'authenticated' | 'unauthenticated') {
  vi.mocked(useAuth).mockReturnValue({
    status,
    user: null,
    register: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    logoutAll: vi.fn(),
  })
}

function renderGuarded(initialPath = '/tasks') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/login" element={<p>Login page</p>} />
        <Route element={<RequireAuth />}>
          <Route path="/tasks" element={<p>Protected tasks page</p>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

describe('RequireAuth', () => {
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('shows a loading placeholder while the session is still being checked — no redirect yet', () => {
    mockStatus('loading')
    renderGuarded()

    expect(screen.queryByText('Protected tasks page')).not.toBeInTheDocument()
    expect(screen.queryByText('Login page')).not.toBeInTheDocument()
  })

  it('redirects to /login when unauthenticated', () => {
    mockStatus('unauthenticated')
    renderGuarded()

    expect(screen.getByText('Login page')).toBeInTheDocument()
    expect(screen.queryByText('Protected tasks page')).not.toBeInTheDocument()
  })

  it('renders the protected route when authenticated', () => {
    mockStatus('authenticated')
    renderGuarded()

    expect(screen.getByText('Protected tasks page')).toBeInTheDocument()
  })
})
