/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateCsrfToken } from '../../api/client'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { TaskStatusControls } from './TaskStatusControls'
import type { Task } from './useTasks'

describe('TaskStatusControls.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'TaskStatusControls.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'TaskStatusControls.module.css')
  })
})

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function makeTask(status: Task['status']): Task {
  return {
    id: 't1',
    title: 'Buy groceries',
    description: '',
    status,
    priority: 'medium',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

describe('TaskStatusControls', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    invalidateCsrfToken()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('pending: all three other statuses are legal targets', () => {
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={vi.fn()} />)

    expect(screen.getByRole('button', { name: 'Move to In progress' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Move to Done' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Move to Cancelled' })).toBeEnabled()
  })

  it('done: only pending and in_progress are legal — cancelled is disabled, not clickable', () => {
    render(<TaskStatusControls task={makeTask('done')} onSuccess={vi.fn()} />)

    expect(screen.getByRole('button', { name: 'Move to Pending' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Move to In progress' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Move to Cancelled' })).toBeDisabled()
  })

  it('cancelled: only pending is legal — done and in_progress are disabled', () => {
    render(<TaskStatusControls task={makeTask('cancelled')} onSuccess={vi.fn()} />)

    expect(screen.getByRole('button', { name: 'Move to Pending' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Move to In progress' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Move to Done' })).toBeDisabled()
  })

  it('clicking a disabled (illegal) button never fires a request', async () => {
    const fetchMock = vi.mocked(fetch)
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('cancelled')} onSuccess={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: 'Move to Done' }))

    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('clicking a legal transition PATCHes /status and calls onSuccess with the result', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    const updated = makeTask('in_progress')
    fetchMock.mockResolvedValueOnce(jsonResponse(200, updated))
    const onSuccess = vi.fn()
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={onSuccess} />)

    await user.click(screen.getByRole('button', { name: 'Move to In progress' }))

    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledWith(updated))
    const [url, init] = fetchMock.mock.calls[1]!
    expect(String(url)).toContain('/v1/tasks/t1/status')
    expect(JSON.parse(init!.body as string)).toEqual({ status: 'in_progress' })
  })

  it('a 409 on a transition the client considered legal is still handled — the mirror is not trusted blindly', async () => {
    // Deliberately tricks the client-side mirror: pending → done is
    // legal per LEGAL_TRANSITIONS, so the button is enabled, but the
    // server disagrees here anyway (simulating a stale mirror — e.g.
    // another tab already moved this task to 'cancelled' first).
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(
      jsonResponse(409, {
        error: 'invalid status transition: cannot move from "cancelled" to "done"',
      }),
    )
    const onSuccess = vi.fn()
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={onSuccess} />)

    await user.click(screen.getByRole('button', { name: 'Move to Done' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/no longer allowed/i)
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it('a 409 concurrency conflict gets the generic "someone else changed this" message', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(
      jsonResponse(409, { error: 'task was modified concurrently, please retry' }),
    )
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: 'Move to Done' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Someone else changed this task at the same time. Please try again.',
    )
  })
})
