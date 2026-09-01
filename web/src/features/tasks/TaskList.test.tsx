/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { TaskList } from './TaskList'
import { useTasks } from './useTasks'

vi.mock('./useTasks', () => ({ useTasks: vi.fn() }))

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
    ...overrides,
  })
}

describe('TaskList', () => {
  afterEach(() => {
    vi.clearAllMocks()
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
})
