import { useCallback, useEffect, useState } from 'react'
import { apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import type { components } from '../../api/types'

export type Task = components['schemas']['Task']

export type TasksStatus = 'loading' | 'empty' | 'error' | 'success'

export interface UseTasksResult {
  status: TasksStatus
  /** The current page's tasks — never an accumulation of the pages before it. */
  tasks: Task[]
  error: ApiError | null
  /** 1-based, for display. There is no total, so there is no "of N" — see the module doc comment. */
  page: number
  /** True when a page came back with more than PAGE_SIZE items — see the module doc comment. */
  hasNextPage: boolean
  hasPreviousPage: boolean
  isPaging: boolean
  nextPage: () => void
  previousPage: () => void
  reload: () => void
  /**
   * Edits patch the current page in place: the mutation response already
   * carries the updated Task, and an edit changes neither the size of
   * the result set nor its created_at ordering, so the window this page
   * represents is still the right one.
   *
   * Create and delete do change the set, and therefore which rows fall
   * inside the window — so they re-fetch the page rather than splicing
   * an 11th row into a page of ten. Cheap now that a page is ten rows
   * rather than everything scrolled past.
   */
  addTaskLocally: (task: Task) => void
  updateTaskLocally: (task: Task) => void
  removeTaskLocally: (id: string) => void
}

// Ten rows a page — small enough that the whole page is on screen at
// once, which is the point of paging rather than accumulating. Exported
// so the tests assert against the same constant rather than a magic
// number duplicated in a test file.
export const PAGE_SIZE = 10

/** "pending,done" -> ["pending", "done"]; "" -> [] (no filter). */
function splitFilter(value: string): string[] {
  return value ? value.split(',').filter(Boolean) : []
}

/**
 * Discrete pages, forwards and back — one window of PAGE_SIZE rows at a
 * time, never an accumulation. Replaces the accumulate-and-scroll model
 * this hook shipped with: that one grew without bound, so a long list
 * meant an ever-heavier page and no way to get back to where you were.
 *
 * GET /v1/tasks has no total count in its response — no X-Total-Count
 * header, no envelope (see docs/openapi.yaml: it returns a bare Task[]).
 * There is structurally no way to render "page 3 of 12", and pretending
 * otherwise would be lying about what the API can tell the client. So
 * the pager shows which page you are on and whether another exists, and
 * nothing it cannot know.
 *
 * Whether another exists comes from the "ask for one extra" technique
 * (AM-4, see docs/changes/web-frontend/plan.md's CI-7 entry and
 * validation.md): request `limit + 1`. If `limit + 1` come back, there
 * is at least one more page — render the first `limit` and set
 * hasNextPage; the extra row is discarded, never rendered, and
 * re-requested as row 1 of the next page.
 */
/**
 * statusFilter/priorityFilter mirror GET /v1/tasks's own query params
 * (docs/openapi.yaml) — "" means "no filter on this field", the same
 * sentinel the backend uses (CI-14).
 *
 * Each is a comma-separated list ("pending,done"), sent to the API as
 * one repeated parameter per value, because the endpoint takes several
 * and ORs them. A string rather than the string[] the caller naturally
 * has: fetchPage's dependency array compares by value, and an array
 * prop is a new identity on every render, which would re-fetch forever.
 * Splitting is this hook's job precisely so no caller has to know that.
 */
export function useTasks(statusFilter = '', priorityFilter = ''): UseTasksResult {
  const [tasks, setTasks] = useState<Task[]>([])
  const [phase, setPhase] = useState<'loading' | 'loaded' | 'error'>('loading')
  const [error, setError] = useState<ApiError | null>(null)
  const [hasNextPage, setHasNextPage] = useState(false)
  // 0-based internally, 1-based only where it is shown.
  const [pageIndex, setPageIndex] = useState(0)

  const fetchPage = useCallback(
    async (index: number) => {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE + 1),
        offset: String(index * PAGE_SIZE),
      })
      // append, not set: the endpoint reads the parameter as a list.
      for (const status of splitFilter(statusFilter)) params.append('status', status)
      for (const priority of splitFilter(priorityFilter)) params.append('priority', priority)

      const response = await apiFetch(`/v1/tasks?${params.toString()}`)
      if (!response.ok) {
        setError(await classifyError(response))
        setPhase('error')
        return
      }
      const page = (await response.json()) as Task[]
      const more = page.length > PAGE_SIZE

      // Deleting the last row of the last page leaves you standing on a
      // page that no longer exists. Step back rather than showing an
      // empty list with a Previous button as the only way out.
      if (page.length === 0 && index > 0) {
        setPageIndex(index - 1)
        return
      }

      setTasks(more ? page.slice(0, PAGE_SIZE) : page)
      setHasNextPage(more)
      setError(null)
      setPhase('loaded')
    },
    [statusFilter, priorityFilter],
  )

  // oxlint's set-state-in-effect rule flags this — it can't see through
  // the async boundary inside fetchPage. Fetching on mount is exactly
  // the "synchronizing with an external system" (the server) the rule's
  // own guidance carves out; accepted, not a bug. fetchPage's identity
  // changes whenever statusFilter/priorityFilter change (see its own
  // useCallback deps above), which is what makes this effect also
  // re-fetch — from offset 0, discarding whatever was scrolled past —
  // every time the caller changes either filter, not just on mount.
  // Changing a filter changes what page 1 even means, so the window
  // resets — during render, not in an effect. As an effect this raced
  // the fetch below: both run on a filter change, and the fetch saw the
  // *previous* pageIndex, firing a request for (say) offset 10 of the
  // newly filtered set before the reset landed and it re-fired at
  // offset 0. Adjusting state during render instead means the fetch
  // effect never observes the stale page at all — React's documented
  // way to derive state from changed inputs.
  const filterKey = `${statusFilter}|${priorityFilter}`
  const [lastFilterKey, setLastFilterKey] = useState(filterKey)
  if (filterKey !== lastFilterKey) {
    setLastFilterKey(filterKey)
    setPageIndex(0)
  }

  useEffect(() => {
    setPhase('loading')
    void fetchPage(pageIndex)
  }, [fetchPage, pageIndex])

  // A page is in flight exactly when the fetch effect above is loading
  // one. Derived rather than tracked: as its own state it needed an
  // effect to clear it, which is the "cascading render" this codebase's
  // linter rightly complains about — and a second source of truth for
  // something `phase` already knows.
  const isPaging = phase === 'loading'

  const nextPage = useCallback(() => {
    if (!hasNextPage) return
    setPageIndex((index) => index + 1)
  }, [hasNextPage])

  const previousPage = useCallback(() => {
    setPageIndex((index) => Math.max(0, index - 1))
  }, [])

  const reload = useCallback(() => {
    setPhase('loading')
    void fetchPage(pageIndex)
  }, [fetchPage, pageIndex])

  // A new task is the newest, so under created_at-ascending ordering it
  // belongs on the *last* page — which is usually not the one being
  // looked at. Splicing it into the current page would show an eleventh
  // row on a page of ten and put it in the wrong place besides, so the
  // page is re-fetched instead. (The task argument stays in the
  // signature: the caller has it, and this hook owning the decision of
  // what to do with it is the point.)
  const addTaskLocally = useCallback(() => {
    void fetchPage(pageIndex)
  }, [fetchPage, pageIndex])

  // An edit usually just changes what a row says, so it patches in
  // place. But an edit can also change whether the row belongs here at
  // all: cancel a task while cancelled is filtered out and it no longer
  // matches, yet patching would leave it sitting on screen until
  // something else caused a fetch. So the updated task is re-checked
  // against the active filter, and a row that no longer matches
  // re-fetches the page instead — which also pulls in whatever row
  // moved up to take its place.
  const updateTaskLocally = useCallback(
    (task: Task) => {
      const wantedStatuses = splitFilter(statusFilter)
      const wantedPriorities = splitFilter(priorityFilter)
      const stillMatches =
        (wantedStatuses.length === 0 || wantedStatuses.includes(task.status)) &&
        (wantedPriorities.length === 0 || wantedPriorities.includes(task.priority))

      if (!stillMatches) {
        void fetchPage(pageIndex)
        return
      }
      setTasks((previous) => previous.map((t) => (t.id === task.id ? task : t)))
    },
    [fetchPage, pageIndex, statusFilter, priorityFilter],
  )

  // A delete pulls every later row one place forward, so the window
  // this page represents now holds a different set — re-fetch rather
  // than leave a nine-row page with a tenth row sitting on the next one
  // that will never be seen.
  const removeTaskLocally = useCallback(() => {
    void fetchPage(pageIndex)
  }, [fetchPage, pageIndex])

  const status: TasksStatus =
    phase === 'loading'
      ? 'loading'
      : phase === 'error'
        ? 'error'
        : tasks.length === 0
          ? 'empty'
          : 'success'

  return {
    status,
    tasks,
    error,
    page: pageIndex + 1,
    hasNextPage,
    hasPreviousPage: pageIndex > 0,
    isPaging,
    nextPage,
    previousPage,
    reload,
    addTaskLocally,
    updateTaskLocally,
    removeTaskLocally,
  }
}
