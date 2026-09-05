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

async function openMenu(user: ReturnType<typeof userEvent.setup>, taskTitle: string) {
  await user.click(screen.getByRole('button', { name: new RegExp(`Change status of "${taskTitle}"`) }))
}

describe('TaskStatusControls', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    invalidateCsrfToken()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('pending: the menu lists all three other statuses as legal targets', async () => {
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={vi.fn()} />)

    await openMenu(user, 'Buy groceries')

    expect(screen.getByRole('menuitem', { name: 'Move to In progress' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Move to Done' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Move to Cancelled' })).toBeInTheDocument()
  })

  it('done: only pending and in_progress are legal — cancelled is absent from the menu entirely', async () => {
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('done')} onSuccess={vi.fn()} />)

    await openMenu(user, 'Buy groceries')

    expect(screen.getByRole('menuitem', { name: 'Move to Pending' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Move to In progress' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Move to Cancelled' })).not.toBeInTheDocument()
  })

  it('cancelled: only pending is legal — done and in_progress are absent from the menu', async () => {
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('cancelled')} onSuccess={vi.fn()} />)

    await openMenu(user, 'Buy groceries')

    expect(screen.getByRole('menuitem', { name: 'Move to Pending' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Move to In progress' })).not.toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Move to Done' })).not.toBeInTheDocument()
  })

  it('selecting a legal transition PATCHes /status and calls onSuccess with the result', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    const updated = makeTask('in_progress')
    fetchMock.mockResolvedValueOnce(jsonResponse(200, updated))
    const onSuccess = vi.fn()
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={onSuccess} />)

    await openMenu(user, 'Buy groceries')
    await user.click(screen.getByRole('menuitem', { name: 'Move to In progress' }))

    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledWith(updated))
    const [url, init] = fetchMock.mock.calls[1]!
    expect(String(url)).toContain('/v1/tasks/t1/status')
    expect(JSON.parse(init!.body as string)).toEqual({ status: 'in_progress' })
  })

  it('a 409 on a transition the client considered legal is still handled — the mirror is not trusted blindly', async () => {
    // Deliberately tricks the client-side mirror: pending → done is
    // legal per LEGAL_TRANSITIONS, so the menu offers it, but the
    // server disagrees here anyway (simulating a stale mirror — e.g.
    // another tab already moved this task to 'cancelled' first).
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(
      jsonResponse(409, {
        error: 'invalid status transition: cannot move from "cancelled" to "done"',
        // Real PATCH /status responses carry this (issue #153) — a mock
        // missing it would test a shape the real backend never sends.
        reason: 'invalid_transition',
      }),
    )
    const onSuccess = vi.fn()
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={onSuccess} />)

    await openMenu(user, 'Buy groceries')
    await user.click(screen.getByRole('menuitem', { name: 'Move to Done' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/no longer allowed/i)
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it('a 409 concurrency conflict gets the generic "someone else changed this" message', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(
      jsonResponse(409, { error: 'task was modified concurrently, please retry', reason: 'concurrency' }),
    )
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={vi.fn()} />)

    await openMenu(user, 'Buy groceries')
    await user.click(screen.getByRole('menuitem', { name: 'Move to Done' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Someone else changed this task at the same time. Please try again.',
    )
  })

  it('while a transition is in flight, the trigger is disabled and marked busy', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    let resolveTransition!: (value: Response) => void
    fetchMock.mockReturnValueOnce(new Promise((resolve) => (resolveTransition = resolve)))
    const user = userEvent.setup()
    render(<TaskStatusControls task={makeTask('pending')} onSuccess={vi.fn()} />)

    await openMenu(user, 'Buy groceries')
    await user.click(screen.getByRole('menuitem', { name: 'Move to Done' }))

    const trigger = screen.getByRole('button', { name: /Change status of "Buy groceries"/ })
    expect(trigger).toBeDisabled()
    expect(trigger).toHaveAttribute('aria-busy', 'true')

    resolveTransition(jsonResponse(200, makeTask('done')))
    await vi.waitFor(() => expect(trigger).not.toBeDisabled())
  })
})
