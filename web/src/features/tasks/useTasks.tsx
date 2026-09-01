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

  const status: TasksStatus =
    phase === 'loading'
      ? 'loading'
      : phase === 'error'
        ? 'error'
        : tasks.length === 0
          ? 'empty'
          : 'success'

  return { status, tasks, error, hasMore, isLoadingMore, loadMore, reload }
}
