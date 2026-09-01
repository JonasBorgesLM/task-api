import type { ReactNode } from 'react'
import { createContext, useContext, useEffect, useState } from 'react'
import { apiFetch, setUnauthorizedHandler } from '../../api/client'
import type { components } from '../../api/types'

export type User = components['schemas']['User']

export type AuthStatus = 'loading' | 'authenticated' | 'unauthenticated'

interface AuthContextValue {
  status: AuthStatus
  user: User | null
  register: (email: string, password: string) => Promise<void>
  login: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
  logoutAll: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

/**
 * Provides session state app-wide. Mounted once, in main.tsx, above the
 * router — every route needs to know whether there's a session before
 * deciding what to render (see RequireAuth).
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>('loading')
  const [user, setUser] = useState<User | null>(null)

  useEffect(() => {
    // The cookie is httpOnly — this app has no way to read it, so
    // GET /auth/me on boot is the only way to know whether a session
    // already exists (e.g. the user reloaded the page).
    apiFetch('/v1/auth/me').then(async (response) => {
      if (response.ok) {
        setUser((await response.json()) as User)
        setStatus('authenticated')
      } else {
        setStatus('unauthenticated')
      }
    })

    // Any request anywhere in the app that comes back 401 means the
    // session is gone (expired, revoked from another device via
    // logout-all, etc.) — not just the ones this hook makes itself. See
    // client.ts's own doc comment on why this callback exists.
    setUnauthorizedHandler(() => {
      setUser(null)
      setStatus('unauthenticated')
    })
    return () => setUnauthorizedHandler(null)
  }, [])

  async function register(email: string, password: string): Promise<void> {
    const response = await apiFetch('/v1/auth/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    })
    if (!response.ok) {
      // Thrown as the raw Response, not a pre-stringified message — the
      // caller (RegisterPage) runs it through errors.ts's classifyError
      // to decide what to show, the same way every other API failure in
      // this app is handled. Keeps "classify a Response" in exactly one
      // place instead of duplicating message-extraction here too.
      throw response
    }
    // Deliberately does not authenticate — see docs/openapi.yaml's
    // register description: "Does not log the caller in." The caller
    // (RegisterPage) sends the user to /login next.
  }

  async function login(email: string, password: string): Promise<void> {
    const response = await apiFetch('/v1/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    })
    if (!response.ok) {
      throw response
    }
    const body = (await response.json()) as { user: User }
    setUser(body.user)
    setStatus('authenticated')
  }

  async function logout(): Promise<void> {
    await apiFetch('/v1/auth/logout', { method: 'POST' })
    // 204 and 401 both mean "the session is gone" — see
    // docs/openapi.yaml's own framing on POST /auth/logout. Clearing
    // state unconditionally matches what the user asked for either way.
    setUser(null)
    setStatus('unauthenticated')
  }

  async function logoutAll(): Promise<void> {
    await apiFetch('/v1/auth/logout-all', { method: 'POST' })
    setUser(null)
    setStatus('unauthenticated')
  }

  return (
    <AuthContext.Provider value={{ status, user, register, login, logout, logoutAll }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return context
}
