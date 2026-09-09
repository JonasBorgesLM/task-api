/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateCsrfToken } from '../../api/client'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { useAuth } from '../auth/useAuth'
import { TaskList } from './TaskList'
import { useTasks } from './useTasks'

// TaskList now calls useNavigate() (the Print button, issue #246) —
// needs a Router context to render at all, even in tests that never
// touch navigation themselves.
function renderTaskList() {
  return render(
    <MemoryRouter>
      <TaskList />
    </MemoryRouter>,
  )
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

vi.mock('./useTasks', () => ({
  useTasks: vi.fn(),
  // Real implementation, not mocked: it's the pure "pending,done" ->
  // ["pending", "done"] splitter handleExport reuses to build the same
  // query string useTasks' own (mocked-away) fetch would have.
  splitFilter: (value: string) => (value ? value.split(',').filter(Boolean) : []),
}))
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
    page: 1,
    hasNextPage: false,
    hasPreviousPage: false,
    isPaging: false,
    nextPage: vi.fn(),
    previousPage: vi.fn(),
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
    renderTaskList()

    expect(screen.getByRole('list', { name: 'Loading tasks' })).toHaveAttribute('aria-busy', 'true')
    expect(screen.queryByText("You don't have any tasks yet.")).not.toBeInTheDocument()
  })

  it('empty: shows the empty-state message, not a spinner or an empty list', () => {
    mockTasksResult({ status: 'empty', tasks: [] })
    renderTaskList()

    expect(screen.getByText(/You don't have any tasks yet/)).toBeInTheDocument()
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
    renderTaskList()

    expect(screen.getByRole('alert')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(reload).toHaveBeenCalledOnce()
  })

  it('success: renders each task with its title, status and priority', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    renderTaskList()

    expect(screen.getByRole('heading', { name: 'Buy groceries' })).toBeInTheDocument()
    expect(screen.getByText('pending')).toBeInTheDocument()
    expect(screen.getByText('high')).toBeInTheDocument()
    // Descriptions live behind each row's own disclosure now — see
    // TaskItem.test.tsx for that behaviour.
    expect(screen.queryByText('Milk, eggs, bread')).not.toBeInTheDocument()
  })

  // The page title and its always-visible count were dropped in the
  // design review: the title named the only screen this app has, and
  // the count now lives one glyph away, in the counts panel.
  it('the counts button names how many are on the page, without opening anything', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask(), makeTask({ id: 't2' })] })
    renderTaskList()

    expect(screen.getByRole('button', { name: 'Task counts (2 on this page)' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Tasks' })).not.toBeInTheDocument()
  })

  // The panel counts the page on screen and says so. It never implies a
  // total, because GET /v1/tasks does not return one.
  it('the counts panel describes the page on screen, not a total', async () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()], hasNextPage: true })
    const user = userEvent.setup()
    renderTaskList()

    await user.click(screen.getByRole('button', { name: 'Task counts (1 on this page)' }))

    expect(screen.getByText('on this page')).toBeInTheDocument()
  })

  it('the counts panel breaks the loaded tasks down by status and priority', async () => {
    mockTasksResult({
      status: 'success',
      tasks: [
        makeTask({ id: 't1', status: 'pending', priority: 'high' }),
        makeTask({ id: 't2', status: 'done', priority: 'high' }),
        makeTask({ id: 't3', status: 'pending', priority: 'low' }),
      ],
    })
    const user = userEvent.setup()
    renderTaskList()

    await user.click(screen.getByRole('button', { name: /^Task counts/ }))

    // Scoped to each group: "Pending"/"High" are also filter options.
    const byStatus = within(screen.getByText('By status').parentElement!.querySelector('dl')!)
    expect(within(byStatus.getByText('Pending').closest('div')!).getByText('2')).toBeInTheDocument()
    expect(within(byStatus.getByText('Done').closest('div')!).getByText('1')).toBeInTheDocument()
    expect(
      within(byStatus.getByText('Cancelled').closest('div')!).getByText('0'),
    ).toBeInTheDocument()

    const byPriority = within(
      screen.getByText('By priority').parentElement!.querySelector('dl')!,
    )
    expect(within(byPriority.getByText('High').closest('div')!).getByText('2')).toBeInTheDocument()
    expect(within(byPriority.getByText('Low').closest('div')!).getByText('1')).toBeInTheDocument()
  })

  // The caption that used to spell out "sorted by creation date, oldest
  // first" is gone (design review — it explained a default nobody can
  // change, on every render). What it was really guarding is unchanged
  // and still asserted below: there is no sort control, and both filter
  // controls exist.
  it('success: sort order is fixed (no sort control), but status/priority filters exist', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    renderTaskList()

    expect(screen.queryByRole('combobox', { name: /sort/i })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Filter by status' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Filter by priority' })).toBeInTheDocument()
  })

  // Cancelled is off by default, so the default view is a filter — the
  // three active statuses, named explicitly on the wire.
  it('defaults to the three active statuses, leaving cancelled out', () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    renderTaskList()

    expect(useTasks).toHaveBeenLastCalledWith('pending,in_progress,done', '')
    expect(screen.getByRole('button', { name: 'Filter by status' })).toHaveTextContent(
      '3 statuses',
    )
  })

  it('ticking cancelled adds it to the filter without removing the others', async () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    const user = userEvent.setup()
    renderTaskList()

    await user.click(screen.getByRole('button', { name: 'Filter by status' }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: 'Cancelled' }))

    // All four selected is the same result set as no filter at all, and
    // is sent as one — see TaskList's own comment.
    expect(useTasks).toHaveBeenLastCalledWith('', '')
    expect(screen.getByRole('button', { name: 'Filter by status' })).toHaveTextContent(
      'All statuses',
    )
  })

  it('unticking a status narrows the filter, and the menu stays open for the next one', async () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    const user = userEvent.setup()
    renderTaskList()

    await user.click(screen.getByRole('button', { name: 'Filter by status' }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: 'Done' }))
    expect(useTasks).toHaveBeenLastCalledWith('pending,in_progress', '')

    // Still open — the point of a multiple choice.
    await user.click(screen.getByRole('menuitemcheckbox', { name: 'In progress' }))
    expect(useTasks).toHaveBeenLastCalledWith('pending', '')
  })

  it('refuses to untick the last remaining value — an empty filter would mean "everything"', async () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    const user = userEvent.setup()
    renderTaskList()

    await user.click(screen.getByRole('button', { name: 'Filter by priority' }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: 'High' }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: 'Medium' }))
    expect(useTasks).toHaveBeenLastCalledWith('pending,in_progress,done', 'low')

    await user.click(screen.getByRole('menuitemcheckbox', { name: 'Low' }))

    expect(useTasks).toHaveBeenLastCalledWith('pending,in_progress,done', 'low')
    expect(screen.getByRole('menuitemcheckbox', { name: 'Low' })).toBeChecked()
  })

  it('empty: shows a filter-specific message once a filter is active', async () => {
    mockTasksResult({ status: 'empty', tasks: [] })
    const user = userEvent.setup()
    renderTaskList()

    // The default already excludes cancelled, so ticking it back on is
    // what "no filter" looks like here.
    await user.click(screen.getByRole('button', { name: 'Filter by status' }))
    // The default view says both things it honestly can: nothing here,
    // and cancelled is hidden.
    expect(screen.getByText(/You don't have any tasks yet/)).toBeInTheDocument()

    await user.click(screen.getByRole('menuitemcheckbox', { name: 'Cancelled' }))

    // Now the user has widened it themselves, so the message is about
    // the filter rather than about having nothing.
    expect(screen.getByText('No tasks match this filter.')).toBeInTheDocument()
    expect(screen.queryByText(/You don't have any tasks yet/)).not.toBeInTheDocument()
  })

  it('Next is offered when another page exists, and calls nextPage on click', async () => {
    const nextPage = vi.fn()
    mockTasksResult({ status: 'success', tasks: [makeTask()], hasNextPage: true, nextPage })
    const user = userEvent.setup()
    renderTaskList()

    const button = screen.getByRole('button', { name: 'Next' })
    await user.click(button)
    expect(nextPage).toHaveBeenCalledOnce()
  })

  it('Previous is offered once off the first page, and calls previousPage on click', async () => {
    const previousPage = vi.fn()
    mockTasksResult({
      status: 'success',
      tasks: [makeTask()],
      page: 3,
      hasPreviousPage: true,
      previousPage,
    })
    const user = userEvent.setup()
    renderTaskList()

    expect(screen.getByText('Page 3')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Previous' }))

    expect(previousPage).toHaveBeenCalledOnce()
  })

  it('disables the direction that has nowhere to go', async () => {
    mockTasksResult({
      status: 'success',
      tasks: [makeTask()],
      hasPreviousPage: false,
      hasNextPage: true,
    })
    renderTaskList()

    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled()
  })

  // A single page needs no pager at all — the limit+1 technique found no
  // extra item, and there is nothing behind it.
  it('hides the pager entirely when the whole result set fits one page', () => {
    mockTasksResult({
      status: 'success',
      tasks: [makeTask()],
      hasNextPage: false,
      hasPreviousPage: false,
    })
    renderTaskList()

    expect(screen.queryByRole('navigation', { name: 'Task pages' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Next' })).not.toBeInTheDocument()
  })

  it('offers "New task" even with zero tasks — creating the first one isn\'t blocked by the empty state', () => {
    mockTasksResult({ status: 'empty', tasks: [] })
    renderTaskList()

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
    renderTaskList()

    await user.click(screen.getByRole('button', { name: 'New task' }))
    expect(screen.getByRole('dialog', { name: 'New task' })).toBeInTheDocument()

    await user.type(screen.getByLabelText(/^Title/), 'New task')
    await user.click(screen.getByRole('button', { name: 'Create task' }))

    await vi.waitFor(() => expect(addTaskLocally).toHaveBeenCalledWith(created))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(await screen.findByText('Task created.')).toBeInTheDocument()
  })

  describe('export', () => {
    beforeEach(() => {
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-url')
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      // A real click would have jsdom attempt navigation (it does not
      // implement one) and log a distracting "not implemented" error;
      // this test only needs to know the link the app built, never that
      // jsdom completed a browser-level download.
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    })

    afterEach(() => {
      vi.restoreAllMocks()
    })

    it('fetches the current filter as CSV and hands the browser a download named from Content-Disposition', async () => {
      const fetchMock = vi.mocked(fetch)
      // A string body, not a Blob: jsdom's Blob polyfill isn't
      // structurally compatible with Node's native Response
      // constructor — see Preview.test.tsx's own comment on the same
      // issue. response.blob() still produces a real, usable Blob.
      fetchMock.mockResolvedValueOnce(
        new Response('id,title\n1,Buy milk\n', {
          status: 200,
          headers: {
            'Content-Type': 'text/csv; charset=utf-8',
            'Content-Disposition': 'attachment; filename="tasks-2026-09-08.csv"',
          },
        }),
      )
      mockTasksResult({ status: 'success', tasks: [makeTask()] })
      const user = userEvent.setup()
      renderTaskList()

      await user.click(screen.getByRole('button', { name: 'Export' }))

      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce())
      const [url] = fetchMock.mock.calls[0]!
      // Default filters: DEFAULT_STATUSES excludes "cancelled" (not all
      // four, so it's sent explicitly); every priority is selected (all
      // three, so the API's own "absent means no filter" applies and
      // none is sent).
      expect(String(url)).toBe(
        'http://localhost:8080/v1/tasks/export?status=pending&status=in_progress&status=done',
      )

      await vi.waitFor(() => expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledOnce())
      expect(URL.createObjectURL).toHaveBeenCalledOnce()
      expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url')
    })

    it('shows the server-provided message when the filter matches too many tasks to export', async () => {
      const fetchMock = vi.mocked(fetch)
      fetchMock.mockResolvedValueOnce(
        jsonResponse(400, {
          error: 'invalid input: 12000 tasks match this filter, more than the 10000-row export limit; narrow the filter',
        }),
      )
      mockTasksResult({ status: 'success', tasks: [makeTask()] })
      const user = userEvent.setup()
      renderTaskList()

      await user.click(screen.getByRole('button', { name: 'Export' }))

      expect(
        await screen.findByText(/more than the 10000-row export limit/),
      ).toBeInTheDocument()
      expect(URL.createObjectURL).not.toHaveBeenCalled()
    })

    it('shows a generic message for a failure with no actionable detail', async () => {
      const fetchMock = vi.mocked(fetch)
      fetchMock.mockResolvedValueOnce(jsonResponse(500, { error: 'internal server error' }))
      mockTasksResult({ status: 'success', tasks: [makeTask()] })
      const user = userEvent.setup()
      renderTaskList()

      await user.click(screen.getByRole('button', { name: 'Export' }))

      expect(await screen.findByText('Could not export tasks. Please try again.')).toBeInTheDocument()
    })
  })

  it('Print opens /report carrying the current filter in the URL', async () => {
    mockTasksResult({ status: 'success', tasks: [makeTask()] })
    const user = userEvent.setup()
    render(
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<TaskList />} />
          {/* A stub, not the real ReportPage — this test only needs to
              know handlePrint navigated with the right query string,
              which is TaskList's own responsibility; ReportPage.test.tsx
              covers what that page does with it. */}
          <Route path="/report" element={<ReportRouteProbe />} />
        </Routes>
      </MemoryRouter>,
    )

    await user.click(screen.getByRole('button', { name: 'Print' }))

    // Default filters: DEFAULT_STATUSES excludes "cancelled"; every
    // priority is selected, so none is sent — same filter shape
    // asserted for Export above.
    expect(await screen.findByText('/report?status=pending&status=in_progress&status=done')).toBeInTheDocument()
  })
})

function ReportRouteProbe() {
  const location = useLocation()
  return <p>{location.pathname + location.search}</p>
}
