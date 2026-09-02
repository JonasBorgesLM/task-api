/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateCsrfToken } from '../../api/client'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { TaskForm } from './TaskForm'
import type { Task } from './useTasks'

describe('TaskForm.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'TaskForm.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'TaskForm.module.css')
  })
})

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const EXISTING_TASK: Task = {
  id: 't1',
  title: 'Buy groceries',
  description: 'Milk, eggs, bread',
  status: 'pending',
  priority: 'high',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

describe('TaskForm', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    invalidateCsrfToken()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('create mode: fields start empty, priority defaults to "not specified"', () => {
    render(<TaskForm onSuccess={vi.fn()} onCancel={vi.fn()} />)

    expect(screen.getByLabelText(/^Title/)).toHaveValue('')
    expect(screen.getByLabelText('Priority')).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Create task' })).toBeInTheDocument()
  })

  it('edit mode: fields prefill from the task, including its real priority', () => {
    render(<TaskForm task={EXISTING_TASK} onSuccess={vi.fn()} onCancel={vi.fn()} />)

    expect(screen.getByLabelText(/^Title/)).toHaveValue('Buy groceries')
    expect(screen.getByLabelText('Description')).toHaveValue('Milk, eggs, bread')
    expect(screen.getByLabelText('Priority')).toHaveValue('high')
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeInTheDocument()
  })

  it('rejects an empty title client-side, without submitting', async () => {
    const fetchMock = vi.mocked(fetch)
    const user = userEvent.setup()
    render(<TaskForm onSuccess={vi.fn()} onCancel={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: 'Create task' }))

    expect(await screen.findByText('Title is required')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('create: submits without a priority field when none was chosen', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, { ...EXISTING_TASK, id: 'new', priority: 'medium' }),
    )
    const onSuccess = vi.fn()
    const user = userEvent.setup()
    render(<TaskForm onSuccess={onSuccess} onCancel={vi.fn()} />)

    await user.type(screen.getByLabelText(/^Title/), 'New task')
    await user.click(screen.getByRole('button', { name: 'Create task' }))

    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalled())
    const [, init] = fetchMock.mock.calls[1]!
    const body = JSON.parse(init!.body as string) as Record<string, unknown>
    expect(body).not.toHaveProperty('priority')
    expect(body).toEqual({ title: 'New task', description: '' })
  })

  it('create: includes priority in the body when the user chose one', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(jsonResponse(201, { ...EXISTING_TASK, id: 'new' }))
    const user = userEvent.setup()
    render(<TaskForm onSuccess={vi.fn()} onCancel={vi.fn()} />)

    await user.type(screen.getByLabelText(/^Title/), 'New task')
    await user.selectOptions(screen.getByLabelText('Priority'), 'low')
    await user.click(screen.getByRole('button', { name: 'Create task' }))

    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    const [, init] = fetchMock.mock.calls[1]!
    const body = JSON.parse(init!.body as string) as Record<string, unknown>
    expect(body).toMatchObject({ priority: 'low' })
  })

  it('edit: PUTs to the task-specific URL and calls onSuccess with the response', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    const updated = { ...EXISTING_TASK, title: 'Buy groceries and cook dinner' }
    fetchMock.mockResolvedValueOnce(jsonResponse(200, updated))
    const onSuccess = vi.fn()
    const user = userEvent.setup()
    render(<TaskForm task={EXISTING_TASK} onSuccess={onSuccess} onCancel={vi.fn()} />)

    await user.clear(screen.getByLabelText(/^Title/))
    await user.type(screen.getByLabelText(/^Title/), 'Buy groceries and cook dinner')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledWith(updated))
    const [url, init] = fetchMock.mock.calls[1]!
    expect(String(url)).toContain('/v1/tasks/t1')
    expect(init!.method).toBe('PUT')
  })

  it('a 409 conflict redisplays the form with a generic message — no diff UI, no lost input', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(
      jsonResponse(409, { error: 'task was modified concurrently, please retry' }),
    )
    const onSuccess = vi.fn()
    const user = userEvent.setup()
    render(<TaskForm task={EXISTING_TASK} onSuccess={onSuccess} onCancel={vi.fn()} />)

    await user.clear(screen.getByLabelText(/^Title/))
    await user.type(screen.getByLabelText(/^Title/), 'A conflicting edit')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Someone else saved a change to this task at the same time. Please try again.',
    )
    expect(onSuccess).not.toHaveBeenCalled()
    // The user's own edit is still there, not thrown away.
    expect(screen.getByLabelText(/^Title/)).toHaveValue('A conflicting edit')
  })

  it('Cancel calls onCancel without submitting', async () => {
    const fetchMock = vi.mocked(fetch)
    const onCancel = vi.fn()
    const user = userEvent.setup()
    render(<TaskForm onSuccess={vi.fn()} onCancel={onCancel} />)

    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(onCancel).toHaveBeenCalledOnce()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
