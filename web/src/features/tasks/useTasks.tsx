import { useCallback, useEffect, useRef, useState } from 'react'
import { apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import type { components } from '../../api/types'

export type Task = components['schemas']['Task']

export type TasksStatus = 'loading' | 'empty' | 'error' | 'success'

export interface UseTasksResult {
  status: TasksStatus
  tasks: Task[]
  error: ApiError | null
  /** True once a page came back with more than PAGE_SIZE items — see the module doc comment. */
  hasMore: boolean
  isLoadingMore: boolean
  loadMore: () => void
  reload: () => void
  /**
   * Local-only mutations for CI-8 (create/edit/delete/status change) —
   * none of them re-fetch. A full reload() after every mutation would
   * be simpler, but it would also throw away everything the user
   * scrolled past via loadMore(), for no reason: the mutation response
   * already carries the updated Task, so patching the array in place is
   * both cheaper and less disruptive.
   */
  addTaskLocally: (task: Task) => void
  updateTaskLocally: (task: Task) => void
  removeTaskLocally: (id: string) => void
}

// No hard reason this has to be exactly 20 — it's a reasonable page size,
// exported so the test can assert against the same constant rather than
// a magic number duplicated in the test file.
export const PAGE_SIZE = 20

/**
 * GET /v1/tasks has no total count in its response — no X-Total-Count
 * header, no envelope (see docs/openapi.yaml: it returns a bare Task[]).
 * There is structurally no way to render "page 3 of 12", and pretending
 * otherwise would be lying about what the API can tell the client. This
 * hook uses the "ask for one extra" technique instead (AM-4, see
 * docs/changes/web-frontend/plan.md's CI-7 entry and validation.md):
 * request `limit + 1`. If exactly `limit + 1` come back, there is at
 * least one more page — display only the first `limit` and set
 * hasMore=true; the (limit+1)th item is discarded, never rendered, and
 * re-requested (as item 1 of the next page) when the caller asks for
 * more.
 */
export function useTasks(): UseTasksResult {
  const [tasks, setTasks] = useState<Task[]>([])
  const [phase, setPhase] = useState<'loading' | 'loaded' | 'error'>('loading')
  const [error, setError] = useState<ApiError | null>(null)
  const [hasMore, setHasMore] = useState(false)
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const nextOffset = useRef(0)

  const fetchPage = useCallback(async (offset: number, append: boolean) => {
    const response = await apiFetch(`/v1/tasks?limit=${PAGE_SIZE + 1}&offset=${offset}`)
    if (!response.ok) {
      setError(await classifyError(response))
      setPhase('error')
      return
    }
    const page = (await response.json()) as Task[]
    const more = page.length > PAGE_SIZE
    const visible = more ? page.slice(0, PAGE_SIZE) : page

    setTasks((previous) => (append ? [...previous, ...visible] : visible))
    setHasMore(more)
    setError(null)
    setPhase('loaded')
    nextOffset.current = offset + visible.length
  }, [])

  // oxlint's set-state-in-effect rule flags this — it can't see through
  // the async boundary inside fetchPage. Fetching on mount is exactly
  // the "synchronizing with an external system" (the server) the rule's
  // own guidance carves out; accepted, not a bug.
  useEffect(() => {
    void fetchPage(0, false)
  }, [fetchPage])

  const loadMore = useCallback(() => {
    if (isLoadingMore || !hasMore) return
    setIsLoadingMore(true)
    void fetchPage(nextOffset.current, true).finally(() => setIsLoadingMore(false))
  }, [fetchPage, hasMore, isLoadingMore])

  const reload = useCallback(() => {
    setPhase('loading')
    void fetchPage(0, false)
  }, [fetchPage])

  // Appended at the end, matching the list's own created_at-ascending
  // order for the common case (viewing the first, un-paginated page). A
  // newly created task is always the newest, so it belongs after
  // everything currently loaded — this can only misplace it relative to
  // not-yet-loaded pages if the caller has already scrolled past page 1,
  // a deliberately accepted, minor edge case rather than a reload().
  const addTaskLocally = useCallback((task: Task) => {
    setTasks((previous) => [...previous, task])
  }, [])

  const updateTaskLocally = useCallback((task: Task) => {
    setTasks((previous) => previous.map((t) => (t.id === task.id ? task : t)))
  }, [])

  // nextOffset shifts back by one to keep loadMore's offset roughly
  // aligned with the server-side dataset, which just got one row
  // shorter. Not exact under multiple concurrent deletes, but correct
  // for the common single-delete case, and a drift here only ever
  // costs a duplicate or skipped row on the *next* loadMore — never a
  // wrong row in what's already rendered.
  const removeTaskLocally = useCallback((id: string) => {
    setTasks((previous) => previous.filter((t) => t.id !== id))
    nextOffset.current = Math.max(0, nextOffset.current - 1)
  }, [])

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
    hasMore,
    isLoadingMore,
    loadMore,
    reload,
    addTaskLocally,
    updateTaskLocally,
    removeTaskLocally,
  }
}
