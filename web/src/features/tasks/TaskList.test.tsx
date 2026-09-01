/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateCsrfToken } from '../../api/client'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { useAuth } from '../auth/useAuth'
import { TaskList } from './TaskList'
import { useTasks } from './useTasks'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

vi.mock('./useTasks', () => ({ useTasks: vi.fn() }))
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }))

describe('TaskList.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'TaskList.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'TaskList.module.css')
  })
})

function makeTask(overrides: Partial<ReturnType<typeof useTasks>['tasks'][number]> = {}) {
  return {
    id: 't1',
    title: 'Buy groceries',
    description: 'Milk, eggs, bread',
    status: 'pending' as const,
    priority: 'high' as const,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function mockTasksResult(overrides: Partial<ReturnType<typeof useTasks>>) {
  vi.mocked(useTasks).mockReturnValue({
    status: 'success',
    tasks: [],
    error: null,
    hasMore: false,
    isLoadingMore: false,
    loadMore: vi.fn(),
    reload: vi.fn(),
    addTaskLocally: vi.fn(),
    updateTaskLocally: vi.fn(),
    removeTaskLocally: vi.fn(),
    ...overrides,
  })
}

describe('TaskList', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    invalidateCsrfToken()
    // Every TaskItem row calls useAuth() now (attachments gating) — off
    // here since none of these tests are about attachments; that gating
    // has its own coverage in TaskItem.test.tsx.
    vi.mocked(useAuth).mockReturnValue({
      status: 'authenticated',
      user: {
        id: 'u1',
        email: 'alice@example.com',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        attachments_enabled: false,
      },
      register: vi.fn(),
      login: vi.fn(),
      logout: vi.fn(),
      logoutAll: vi.fn(),
    })
  })

  afterEach(() => {
    vi.clearAllMocks()
    vi.unstubAllGlobals()
  })

  it('loading: shows skeleton placeholders, not the list or a message', () => {
    mockTasksResult({ status: 'loading' })
    render(<TaskList />)

    expect(screen.getByRole('list', { name: 'Loading tasks' })).toHaveAttribute('aria-busy', 'true')
    expect(screen.queryByText("You don't have any tasks yet.")).not.toBeInTheDocument()
  })

  it('empty: shows the empty-state message, not a spinner or an empty list', () => {
    mockTasksResult({ status: 'empty', tasks: [] })
    render(<TaskList />)

    expect(screen.getByText("You don't have any tasks yet.")).toBeInTheDocument()
    expect(screen.queryByRole('list')).not.toBeInTheDocument()
  })

  it('error: shows the error and a Retry button that calls reload', async () => {
    const reload = vi.fn()
    mockTasksResult({
      status: 'error',
      error: {
        requestId: null,
        kind: 'unavailable',
        message: 'service temporarily unavailable, please retry',
      },
      reload,
    })
    const user = userEvent.setup()
    render(<TaskList />)

    expect(screen.getByRole('alert')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(reload).toHaveBeenCalledOnce()
  })

  it('success: renders each task with its title, status and priority', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    render(<TaskList />)

    expect(screen.getByRole('heading', { name: 'Buy groceries' })).toBeInTheDocument()
    expect(screen.getByText('Milk, eggs, bread')).toBeInTheDocument()
    expect(screen.getByText('pending')).toBeInTheDocument()
    expect(screen.getByText('high')).toBeInTheDocument()
  })

  it('success: is explicit that sort/filter is fixed — no UI implying capability the API lacks', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    render(<TaskList />)

    expect(screen.getByText(/sorted by creation date/i)).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: /sort/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: /filter/i })).not.toBeInTheDocument()
  })

  it('shows "Load more" when hasMore is true, and calls loadMore on click', async () => {
    const loadMore = vi.fn()
    mockTasksResult({ status: 'success', tasks: [makeTask()], hasMore: true, loadMore })
    const user = userEvent.setup()
    render(<TaskList />)

    const button = screen.getByRole('button', { name: 'Load more' })
    await user.click(button)
    expect(loadMore).toHaveBeenCalledOnce()
  })

  it('hides "Load more" when hasMore is false — the limit+1 technique found no extra item', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()], hasMore: false })
    render(<TaskList />)

    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
  })

  it('offers "New task" even with zero tasks — creating the first one isn\'t blocked by the empty state', () => {
    mockTasksResult({ status: 'empty', tasks: [] })
    render(<TaskList />)

    expect(screen.getByRole('button', { name: 'New task' })).toBeInTheDocument()
  })

  it('creating a task adds it locally, closes the modal, and shows a success toast', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    const created = makeTask({ id: 'new', title: 'New task' })
    fetchMock.mockResolvedValueOnce(jsonResponse(201, created))
    const addTaskLocally = vi.fn()
    mockTasksResult({ status: 'success', tasks: [makeTask()], addTaskLocally })
    const user = userEvent.setup()
    render(<TaskList />)

    await user.click(screen.getByRole('button', { name: 'New task' }))
    expect(screen.getByRole('dialog', { name: 'New task' })).toBeInTheDocument()

    await user.type(screen.getByLabelText(/^Title/), 'New task')
    await user.click(screen.getByRole('button', { name: 'Create task' }))

    await vi.waitFor(() => expect(addTaskLocally).toHaveBeenCalledWith(created))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(await screen.findByText('Task created.')).toBeInTheDocument()
  })
})
