import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiFetch, invalidateCsrfToken, setUnauthorizedHandler } from './client'

function jsonResponse(status: number, body: unknown, headers?: HeadersInit): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

function textResponse(status: number, body: string): Response {
  return new Response(body, { status, headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
}

const CSRF_TOKEN_RESPONSE = () => jsonResponse(200, { csrf_token: 'test-csrf-token' })

describe('apiFetch', () => {
  beforeEach(() => {
    invalidateCsrfToken()
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    setUnauthorizedHandler(null)
  })

  it('GET sends credentials: include and no X-CSRF-Token header', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))

    await apiFetch('/v1/tasks')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [, init] = fetchMock.mock.calls[0]!
    expect(init?.credentials).toBe('include')
    expect(new Headers(init?.headers).has('X-CSRF-Token')).toBe(false)
  })

  it.each(['POST', 'PUT', 'PATCH', 'DELETE'])('%s fetches a CSRF token and attaches it', async (method) => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE()) // GET /auth/csrf-token
    fetchMock.mockResolvedValueOnce(jsonResponse(201, {})) // the actual request

    await apiFetch('/v1/tasks', { method })

    expect(fetchMock).toHaveBeenCalledTimes(2)
    const [tokenUrl] = fetchMock.mock.calls[0]!
    expect(String(tokenUrl)).toContain('/v1/auth/csrf-token')
    const [, requestInit] = fetchMock.mock.calls[1]!
    expect(new Headers(requestInit?.headers).get('X-CSRF-Token')).toBe('test-csrf-token')
    expect(requestInit?.credentials).toBe('include')
  })

  it('caches the CSRF token across multiple mutating calls', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE())
    fetchMock.mockResolvedValueOnce(jsonResponse(201, {}))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, {}))

    await apiFetch('/v1/tasks', { method: 'POST' })
    await apiFetch('/v1/tasks/abc', { method: 'PATCH' })

    // 1 token fetch + 2 requests = 3 calls, not 4 — the second mutating
    // call must not re-fetch a token it already has cached.
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('on a 403, refetches the CSRF token and retries exactly once', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE()) // initial token fetch
    fetchMock.mockResolvedValueOnce(textResponse(403, 'Forbidden\n')) // stale token rejected
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE()) // refetch after 403
    fetchMock.mockResolvedValueOnce(jsonResponse(201, {})) // retry succeeds

    const response = await apiFetch('/v1/tasks', { method: 'POST' })

    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(response.status).toBe(201)
  })

  it('a 403 that survives the retry is returned as-is, not retried again', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE())
    fetchMock.mockResolvedValueOnce(textResponse(403, 'Forbidden\n'))
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE())
    fetchMock.mockResolvedValueOnce(textResponse(403, 'Forbidden\n'))

    const response = await apiFetch('/v1/tasks', { method: 'POST' })

    // Exactly 4 calls: 1 token + 1 attempt + 1 refetch + 1 retry. A third
    // attempt would mean the retry-once guarantee broke into a loop.
    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(response.status).toBe(403)
  })

  it('a GET never retries on 403 (403 only makes sense for the mutating/CSRF path)', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(textResponse(403, 'Forbidden\n'))

    const response = await apiFetch('/v1/tasks')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(response.status).toBe(403)
  })

  it('calls the registered handler on 401', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(401, { error: 'invalid or expired session token' }))
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)

    await apiFetch('/v1/tasks')

    expect(onUnauthorized).toHaveBeenCalledOnce()
  })

  it('does NOT call the unauthorized handler on 503 — retryable, not a session problem', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(
      jsonResponse(503, { error: 'service temporarily unavailable, please retry' }),
    )
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)

    const response = await apiFetch('/v1/tasks')

    expect(onUnauthorized).not.toHaveBeenCalled()
    expect(response.status).toBe(503)
  })
})
