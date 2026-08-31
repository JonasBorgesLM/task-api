// Classifies a Response into a structured error — nothing more. This
// module doesn't decide what a component shows or does about an error
// (no redirect, no state clearing); client.ts owns those side effects,
// and a future component decides the message/UI. See
// docs/changes/web-frontend/plan.md CI-4.

// Every response in this API carries X-Request-Id (see
// docs/openapi.yaml's XRequestID header, sent even on error responses).
// issue #122: surfacing it on an unexpected error gives the user
// something citable and ties their report straight to a server log
// line — worth carrying on every variant now rather than retrofitting
// it once a component actually needs to render it.
export type ApiError = { requestId: string | null } & (
  | { kind: 'invalid_input'; message: string }
  | { kind: 'unauthorized' }
  | { kind: 'forbidden' }
  | { kind: 'not_found' }
  | { kind: 'conflict'; reason: 'invalid_transition' | 'concurrency' | 'unknown'; message: string }
  | { kind: 'rate_limited'; retryAfterSeconds: number | null }
  // 503 is deliberately its own kind, distinct from 'unknown' or
  // 'unauthorized': it means the session lookup itself couldn't run
  // (database down/unreachable), not that the caller's token is bad.
  // See docs/openapi.yaml's ServiceUnavailable response — "the client
  // must NOT discard its credentials and re-authenticate."
  | { kind: 'unavailable'; message: string }
  | { kind: 'unknown'; status: number }
)

interface ErrorResponseBody {
  error?: string
}

// 403 (CSRF) and 429 (rate limit) are the two exceptions in this API's
// contract to "every error response is the {"error": "..."} JSON
// envelope" — both are written by moat's own default rejection
// handlers (csrf.Protector's and ratelimit.Limiter's), which use
// net/http's http.Error and so respond with plain text. Confirmed by
// reading those libraries' source, not assumed from docs/openapi.yaml
// alone — see this PR's description for the ratelimit case, which
// docs/openapi.yaml still (incorrectly) documents as JSON.
async function readErrorMessage(response: Response): Promise<string | undefined> {
  try {
    const body = (await response.json()) as ErrorResponseBody
    return body.error
  } catch {
    return undefined
  }
}

const INVALID_TRANSITION_MARKER = 'invalid status transition'
const CONCURRENCY_MARKER = 'modified concurrently'

function classifyConflict(message: string | undefined): 'invalid_transition' | 'concurrency' | 'unknown' {
  if (!message) return 'unknown'
  if (message.includes(INVALID_TRANSITION_MARKER)) return 'invalid_transition'
  if (message.includes(CONCURRENCY_MARKER)) return 'concurrency'
  return 'unknown'
}

/**
 * Reads and classifies an error Response. Only meaningful for a
 * non-ok (status >= 400) response — callers are expected to have
 * already checked response.ok.
 */
export async function classifyError(response: Response): Promise<ApiError> {
  const requestId = response.headers.get('X-Request-Id')

  switch (response.status) {
    case 400:
      return {
        requestId,
        kind: 'invalid_input',
        message: (await readErrorMessage(response)) ?? 'invalid request',
      }
    case 401:
      return { requestId, kind: 'unauthorized' }
    case 403:
      return { requestId, kind: 'forbidden' }
    case 404:
      return { requestId, kind: 'not_found' }
    case 409: {
      const message = await readErrorMessage(response)
      return { requestId, kind: 'conflict', reason: classifyConflict(message), message: message ?? '' }
    }
    case 429: {
      const retryAfter = response.headers.get('Retry-After')
      return {
        requestId,
        kind: 'rate_limited',
        retryAfterSeconds: retryAfter === null ? null : Number(retryAfter),
      }
    }
    case 503:
      return {
        requestId,
        kind: 'unavailable',
        message: (await readErrorMessage(response)) ?? 'service unavailable',
      }
    default:
      return { requestId, kind: 'unknown', status: response.status }
  }
}
