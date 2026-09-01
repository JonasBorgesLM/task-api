import type { ReactNode } from 'react'
import { createContext, useContext, useEffect, useState } from 'react'
import { apiFetch, setUnauthorizedHandler } from '../../api/client'
import type { components } from '../../api/types'

// MeResponse, not the bare User schema: GET /auth/me is the only
// endpoint that carries attachments_enabled (dual-auth-mode CI-8) — see
// login() below for why that matters for the *login* response too, not
// just boot hydration.
export type User = components['schemas']['MeResponse']

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

  /**
   * GET /auth/me and apply the result to state. Shared by boot
   * hydration and login() — see login()'s own comment for why login
   * needs this too, not just the cookie the response already set.
   */
  async function hydrate(): Promise<void> {
    const response = await apiFetch('/v1/auth/me')
    if (response.ok) {
      setUser((await response.json()) as User)
      setStatus('authenticated')
    } else {
      setStatus('unauthenticated')
    }
  }

  useEffect(() => {
    // The cookie is httpOnly — this app has no way to read it, so
    // GET /auth/me on boot is the only way to know whether a session
    // already exists (e.g. the user reloaded the page).
    void hydrate()

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
    // LoginResponse.user is the bare User schema — no attachments_enabled
    // (only GET /auth/me's MeResponse carries that). Re-hydrating here,
    // instead of trusting the login response's own user object, is what
    // makes attachments_enabled correct immediately after login rather
    // than only after the next page reload's boot hydration — a real gap
    // found while wiring CI-9's feature detection, not a hypothetical one.
    await hydrate()
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
