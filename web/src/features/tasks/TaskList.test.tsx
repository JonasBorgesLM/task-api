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

  it('success: shows a page title and a count of what is actually loaded', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask(), makeTask({ id: 't2' })] })
    render(<TaskList />)

    expect(screen.getByRole('heading', { name: 'Tasks' })).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()
  })

  it('the count gets a "+" when hasMore is true — never a fake total the API never gave', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()], hasMore: true })
    render(<TaskList />)

    expect(screen.getByText('1+')).toBeInTheDocument()
  })

  it('loading/empty/error states show the page title but no count (nothing loaded yet)', () => {
    mockTasksResult({ status: 'loading' })
    render(<TaskList />)
    expect(screen.queryByRole('heading', { name: 'Tasks' })).not.toBeInTheDocument()

    mockTasksResult({ status: 'empty', tasks: [] })
    render(<TaskList />)
    expect(screen.getByRole('heading', { name: 'Tasks' })).toBeInTheDocument()
    expect(screen.queryByText('0')).not.toBeInTheDocument()
  })

  // The caption that used to spell out "sorted by creation date, oldest
  // first" is gone (design review — it explained a default nobody can
  // change, on every render). What it was really guarding is unchanged
  // and still asserted below: there is no sort control, and both filter
  // controls exist.
  it('success: sort order is fixed (no sort control), but status/priority filters exist', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    render(<TaskList />)

    expect(screen.queryByRole('combobox', { name: /sort/i })).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Status' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Priority' })).toBeInTheDocument()
  })

  it('changing the status filter re-calls useTasks with the new value', async () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    const user = userEvent.setup()
    render(<TaskList />)

    await user.selectOptions(screen.getByRole('combobox', { name: 'Status' }), 'In progress')

    expect(useTasks).toHaveBeenLastCalledWith('in_progress', '')
  })

  it('changing the priority filter re-calls useTasks with the new value', async () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    const user = userEvent.setup()
    render(<TaskList />)

    await user.selectOptions(screen.getByRole('combobox', { name: 'Priority' }), 'Low')

    expect(useTasks).toHaveBeenLastCalledWith('', 'low')
  })

  it('picking "All statuses" again clears the status filter', async () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    const user = userEvent.setup()
    render(<TaskList />)

    const statusSelect = screen.getByRole('combobox', { name: 'Status' })
    await user.selectOptions(statusSelect, 'Done')
    expect(useTasks).toHaveBeenLastCalledWith('done', '')

    await user.selectOptions(statusSelect, 'All statuses')
    expect(useTasks).toHaveBeenLastCalledWith('', '')
  })

  it('empty: shows a filter-specific message once a filter is active', async () => {
    mockTasksResult({ status: 'empty', tasks: [] })
    const user = userEvent.setup()
    render(<TaskList />)

    expect(screen.getByText("You don't have any tasks yet.")).toBeInTheDocument()

    await user.selectOptions(screen.getByRole('combobox', { name: 'Priority' }), 'High')

    expect(screen.getByText('No tasks match this filter.')).toBeInTheDocument()
    expect(screen.queryByText("You don't have any tasks yet.")).not.toBeInTheDocument()
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
