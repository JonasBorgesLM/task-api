import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiFetch, invalidateCsrfToken, setUnauthorizedHandler, uploadFile } from './client'

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

  it.each(['POST', 'PUT', 'PATCH', 'DELETE'])(
    '%s fetches a CSRF token and attaches it',
    async (method) => {
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
    },
  )

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
    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { error: 'invalid or expired session token' }),
    )
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

// jsdom's XMLHttpRequest doesn't hit a real network by itself — this
// stands in for a real server, the same role vi.stubGlobal('fetch', ...)
// plays for apiFetch's tests. It exists because uploadFile deliberately
// doesn't use fetch (see client.ts's doc comment on why), so it needs
// its own harness rather than reusing the fetch mock.
class MockXHR {
  static instances: MockXHR[] = []

  method = ''
  url = ''
  withCredentials = false
  status = 0
  responseText = ''
  requestHeaders: Record<string, string> = {}
  sentBody: FormData | null = null

  private listeners: Record<string, Array<(event: unknown) => void>> = {}
  private uploadListeners: Record<string, Array<(event: unknown) => void>> = {}

  upload = {
    addEventListener: (type: string, fn: (event: unknown) => void) => {
      ;(this.uploadListeners[type] ??= []).push(fn)
    },
  }

  constructor() {
    MockXHR.instances.push(this)
  }

  open(method: string, url: string) {
    this.method = method
    this.url = url
  }

  setRequestHeader(name: string, value: string) {
    this.requestHeaders[name] = value
  }

  getAllResponseHeaders(): string {
    return 'Content-Type: application/json\r\n'
  }

  addEventListener(type: string, fn: (event: unknown) => void) {
    ;(this.listeners[type] ??= []).push(fn)
  }

  send(body: FormData) {
    this.sentBody = body
  }

  simulateUploadProgress(loaded: number, total: number) {
    for (const fn of this.uploadListeners.progress ?? [])
      fn({ lengthComputable: true, loaded, total })
  }

  simulateLoad(status: number, responseText: string) {
    this.status = status
    this.responseText = responseText
    for (const fn of this.listeners.load ?? []) fn({})
  }

  simulateError() {
    for (const fn of this.listeners.error ?? []) fn({})
  }
}

describe('uploadFile', () => {
  beforeEach(() => {
    invalidateCsrfToken()
    vi.stubGlobal('fetch', vi.fn())
    MockXHR.instances = []
    vi.stubGlobal('XMLHttpRequest', MockXHR)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('POSTs multipart form data with credentials and the CSRF header', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE())
    const file = new File(['hello'], 'report.pdf', { type: 'application/pdf' })

    const uploadPromise = uploadFile('/v1/tasks/t1/attachments', file)
    await vi.waitFor(() => expect(MockXHR.instances).toHaveLength(1))
    const xhr = MockXHR.instances[0]!
    xhr.simulateLoad(201, JSON.stringify({ id: 'a1' }))
    const response = await uploadPromise

    expect(xhr.method).toBe('POST')
    expect(xhr.url).toContain('/v1/tasks/t1/attachments')
    expect(xhr.withCredentials).toBe(true)
    expect(xhr.requestHeaders['X-CSRF-Token']).toBe('test-csrf-token')
    expect(xhr.sentBody?.get('file')).toBe(file)
    expect(response.status).toBe(201)
    expect(await response.json()).toEqual({ id: 'a1' })
  })

  it('reports upload progress as it happens', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE())
    const file = new File(['hello'], 'report.pdf', { type: 'application/pdf' })
    const onProgress = vi.fn()

    const uploadPromise = uploadFile('/v1/tasks/t1/attachments', file, onProgress)
    await vi.waitFor(() => expect(MockXHR.instances).toHaveLength(1))
    const xhr = MockXHR.instances[0]!
    xhr.simulateUploadProgress(50, 100)
    xhr.simulateLoad(201, '{}')
    await uploadPromise

    expect(onProgress).toHaveBeenCalledWith({ loaded: 50, total: 100 })
  })

  it('rejects on a network error', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(CSRF_TOKEN_RESPONSE())
    const file = new File(['hello'], 'report.pdf', { type: 'application/pdf' })

    const uploadPromise = uploadFile('/v1/tasks/t1/attachments', file)
    await vi.waitFor(() => expect(MockXHR.instances).toHaveLength(1))
    MockXHR.instances[0]!.simulateError()

    await expect(uploadPromise).rejects.toThrow('network error during upload')
  })
})
