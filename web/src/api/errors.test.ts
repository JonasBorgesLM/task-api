import { describe, expect, it } from 'vitest'
import { classifyError } from './errors'

function jsonResponse(status: number, body: unknown, headers?: HeadersInit): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

function textResponse(status: number, body: string, headers?: HeadersInit): Response {
  return new Response(body, {
    status,
    headers: { 'Content-Type': 'text/plain; charset=utf-8', ...headers },
  })
}

describe('classifyError', () => {
  it('400 → invalid_input, with the server message verbatim', async () => {
    const result = await classifyError(jsonResponse(400, { error: 'invalid input: title must not be empty' }))
    expect(result).toEqual({
      requestId: null,
      kind: 'invalid_input',
      message: 'invalid input: title must not be empty',
    })
  })

  it('401 → unauthorized', async () => {
    const result = await classifyError(jsonResponse(401, { error: 'invalid or expired session token' }))
    expect(result).toEqual({ requestId: null, kind: 'unauthorized' })
  })

  it('403 → forbidden, even with a plain-text body (the real CSRF rejection shape)', async () => {
    // moat/csrf's default rejection handler writes plain text, not the
    // {"error": "..."} envelope — see docs/openapi.yaml's Forbidden
    // response and this repo's PR #139. classifyError must not throw
    // trying to JSON-parse it.
    const result = await classifyError(textResponse(403, 'Forbidden\n'))
    expect(result).toEqual({ requestId: null, kind: 'forbidden' })
  })

  it('404 → not_found', async () => {
    const result = await classifyError(jsonResponse(404, { error: 'task not found' }))
    expect(result).toEqual({ requestId: null, kind: 'not_found' })
  })

  // issue #153: PATCH /tasks/{id}/status's 409 carries a machine-readable
  // "reason" field (docs/openapi.yaml's TransitionConflictResponse) —
  // classifyError reads that directly rather than pattern-matching the
  // human-readable message, which used to be the only way to tell the
  // two conditions apart and broke the moment either message's wording
  // changed for an unrelated reason (a typo fix, a clarity pass).
  it('409 with reason "invalid_transition" → conflict/invalid_transition', async () => {
    const message = 'invalid status transition: cannot move from "cancelled" to "done"'
    const result = await classifyError(jsonResponse(409, { error: message, reason: 'invalid_transition' }))
    expect(result).toEqual({ requestId: null, kind: 'conflict', reason: 'invalid_transition', message })
  })

  it('409 with reason "concurrency" → conflict/concurrency', async () => {
    const message = 'task was modified concurrently, please retry'
    const result = await classifyError(jsonResponse(409, { error: message, reason: 'concurrency' }))
    expect(result).toEqual({ requestId: null, kind: 'conflict', reason: 'concurrency', message })
  })

  // PUT /tasks/{id}'s own 409 deliberately never got this field (see
  // issue #153's "Não faz" — no transition-legality ambiguity there to
  // begin with) — its body is message-only, same as every other error
  // response in this API. A body without "reason" must classify as
  // 'unknown' regardless of what the message text happens to say, not
  // fall back to guessing from it — that guesswork is exactly what this
  // issue replaced.
  it('409 with no reason field → conflict/unknown, even when the message text looks like a transition conflict', async () => {
    const message = 'invalid status transition: cannot move from "cancelled" to "done"'
    const result = await classifyError(jsonResponse(409, { error: message }))
    expect(result).toEqual({ requestId: null, kind: 'conflict', reason: 'unknown', message })
  })

  it('409 with an unrecognized reason value → conflict/unknown, not a thrown error', async () => {
    const result = await classifyError(
      jsonResponse(409, { error: 'something new the API added later', reason: 'something_new' }),
    )
    expect(result).toEqual({
      requestId: null,
      kind: 'conflict',
      reason: 'unknown',
      message: 'something new the API added later',
    })
  })

  it('429 with Retry-After → rate_limited with the parsed seconds', async () => {
    const result = await classifyError(textResponse(429, 'Too Many Requests\n', { 'Retry-After': '3' }))
    expect(result).toEqual({ requestId: null, kind: 'rate_limited', retryAfterSeconds: 3 })
  })

  it('429 without Retry-After → rate_limited with null', async () => {
    const result = await classifyError(textResponse(429, 'Too Many Requests\n'))
    expect(result).toEqual({ requestId: null, kind: 'rate_limited', retryAfterSeconds: null })
  })

  it('503 → unavailable, with the server message', async () => {
    const result = await classifyError(
      jsonResponse(503, { error: 'service temporarily unavailable, please retry' }),
    )
    expect(result).toEqual({
      requestId: null,
      kind: 'unavailable',
      message: 'service temporarily unavailable, please retry',
    })
  })

  it('an unmapped status → unknown, carrying the status code', async () => {
    const result = await classifyError(jsonResponse(418, { error: "I'm a teapot" }))
    expect(result).toEqual({ requestId: null, kind: 'unknown', status: 418 })
  })

  it('carries X-Request-Id through when the response actually has one', async () => {
    // Every response in this API carries this header (see
    // docs/openapi.yaml's XRequestID) — issue #122 wants it surfaced on
    // an unexpected error so a user report can be tied to a server log
    // line.
    const result = await classifyError(
      jsonResponse(500, { error: 'internal server error' }, { 'X-Request-Id': 'req-abc123' }),
    )
    expect(result).toEqual({ requestId: 'req-abc123', kind: 'unknown', status: 500 })
  })
})
