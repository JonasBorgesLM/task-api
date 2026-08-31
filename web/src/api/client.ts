// The only place in this app allowed to call fetch — see
// docs/changes/web-frontend/plan.md CI-4. Every visual component goes
// through apiFetch instead, so credential handling, CSRF, and the
// unauthorized-session hook live in exactly one place.

const API_BASE = import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080'

const MUTATING_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

// The CSRF token lives in memory only — see docs/DECISIONS.md § "Frontend:
// Vite (SPA)..." § "Cookie httpOnly, nunca localStorage". It is never
// written to localStorage/sessionStorage; a page reload loses it and the
// next mutating request fetches a fresh one.
let csrfToken: string | null = null
let csrfTokenPromise: Promise<string> | null = null

interface CsrfTokenResponse {
  csrf_token: string
}

async function fetchCsrfToken(): Promise<string> {
  const response = await fetch(`${API_BASE}/v1/auth/csrf-token`, {
    credentials: 'include',
  })
  if (!response.ok) {
    throw new Error(`failed to fetch CSRF token: ${response.status}`)
  }
  const body = (await response.json()) as CsrfTokenResponse
  return body.csrf_token
}

// Coalesces concurrent callers into a single in-flight request — two
// mutating calls racing on page load must not both trigger their own
// GET /auth/csrf-token.
function getCsrfToken(): Promise<string> {
  if (csrfToken) {
    return Promise.resolve(csrfToken)
  }
  csrfTokenPromise ??= fetchCsrfToken().then((token) => {
    csrfToken = token
    csrfTokenPromise = null
    return token
  })
  return csrfTokenPromise
}

// Discards the cached token so the next getCsrfToken() call fetches a
// fresh one. Exported so a future auth flow (CI-6) can call it
// proactively around a privilege change — see the 403-retry comment in
// apiFetch below for why staleness happens even without that.
export function invalidateCsrfToken(): void {
  csrfToken = null
  csrfTokenPromise = null
}

let onUnauthorized: (() => void) | null = null

// Registers a callback invoked whenever any request returns 401. This
// module does not itself know what "clear session state" or "redirect"
// mean — that is CI-6's useAuth, which owns the session. Passing null
// unregisters. See errors.ts for why 401 is the only status that does
// this: 503 is explicitly NOT treated as "unauthorized" even though it
// also means "this request didn't succeed" — see docs/openapi.yaml's
// ServiceUnavailable response.
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  onUnauthorized = handler
}

async function doFetch(path: string, init: RequestInit, csrfHeader: string | null): Promise<Response> {
  const headers = new Headers(init.headers)
  if (csrfHeader !== null) {
    headers.set('X-CSRF-Token', csrfHeader)
  }
  return fetch(`${API_BASE}${path}`, {
    ...init,
    credentials: 'include',
    headers,
  })
}

/**
 * The only fetch wrapper this app uses. Always sends credentials
 * (the httpOnly session cookie) and, on a mutating method, the CSRF
 * header — never on GET/HEAD/OPTIONS, which the backend's CSRF gate
 * doesn't check anyway (see internal/middleware/csrf.go's isSafeMethod).
 */
export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const method = (init.method ?? 'GET').toUpperCase()
  const isMutating = MUTATING_METHODS.has(method)

  const response = await doFetch(path, { ...init, method }, isMutating ? await getCsrfToken() : null)

  // A mutating request's CSRF token can go stale without this client
  // doing anything wrong: the backend rotates the CSRF cookie on every
  // successful login (see docs/DECISIONS.md § "Autenticação: modo
  // duplo" and moat/csrf's Protector.Rotate), so a token fetched before
  // login no longer matches after it. Refetch once and retry exactly
  // once — a second 403 is a real failure (bad Origin, disabled
  // cookies), not staleness, and is returned as-is for errors.ts to
  // classify.
  if (isMutating && response.status === 403) {
    invalidateCsrfToken()
    const retry = await doFetch(path, { ...init, method }, await getCsrfToken())
    return finalize(retry)
  }

  return finalize(response)
}

function finalize(response: Response): Response {
  if (response.status === 401) {
    onUnauthorized?.()
  }
  return response
}
