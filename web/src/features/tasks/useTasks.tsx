import { useCallback, useEffect, useRef, useState } from 'react'
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
  /** 1-based, for display. */
  page: number
  /** ceil(total / PAGE_SIZE), at least 1 — the page count "Page N of M" needs. */
  totalPages: number
  /** The number of tasks matching the current filter, from GET /v1/tasks's own X-Total-Count (issue #237). */
  total: number
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

/**
 * "pending,done" -> ["pending", "done"]; "" -> [] (no filter). Exported
 * so any other caller building a query string from the same joined
 * filter representation (TaskList's export button) reads it identically
 * to this hook's own fetches, instead of re-deriving the split rule.
 */
export function splitFilter(value: string): string[] {
  return value ? value.split(',').filter(Boolean) : []
}

/**
 * Discrete pages, forwards and back — one window of PAGE_SIZE rows at a
 * time, never an accumulation. Replaces the accumulate-and-scroll model
 * this hook shipped with: that one grew without bound, so a long list
 * meant an ever-heavier page and no way to get back to where you were.
 *
 * GET /v1/tasks now sets X-Total-Count (issue #237) — the number
 * matching the current filter, independent of limit/offset. This hook
 * reads it and derives hasNextPage/totalPages from it directly, which
 * retired the "ask for one extra" technique (request limit + 1, discard
 * the spare row) an earlier version of this hook used when no total was
 * available at all — see issue #247/15.G1. A request now asks for
 * exactly PAGE_SIZE rows.
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
// pageCacheEntry is one (statusFilter, priorityFilter, pageIndex)'s last
// known result — see pageCacheKey and useTasks' cacheRef for how it is
// keyed and invalidated.
interface pageCacheEntry {
  tasks: Task[]
  // X-Total-Count as of this fetch (issue #237) — hasNextPage/totalPages
  // are derived from this and pageIndex, not stored separately, so a
  // revalidation that changes the total (see the 304 branch in
  // fetchPage) only ever needs to update this one field.
  total: number
  // GET /v1/tasks's own ETag (15.D2), or null the first time a page is
  // ever fetched — nothing to send as If-None-Match yet.
  etag: string | null
}

function pageCacheKey(statusFilter: string, priorityFilter: string, index: number): string {
  return `${statusFilter}|${priorityFilter}|${index}`
}

export function useTasks(statusFilter = '', priorityFilter = ''): UseTasksResult {
  const [tasks, setTasks] = useState<Task[]>([])
  const [phase, setPhase] = useState<'loading' | 'loaded' | 'error'>('loading')
  const [error, setError] = useState<ApiError | null>(null)
  const [total, setTotal] = useState(0)
  // 0-based internally, 1-based only where it is shown.
  const [pageIndex, setPageIndex] = useState(0)

  // Tracks the one fetch this hook considers current. fetchPage is
  // called from several places below (the load effect, reload,
  // addTaskLocally, removeTaskLocally, updateTaskLocally's re-fetch
  // path) — switching filters or paging quickly can leave more than one
  // of those in flight at once, and without this the one that happens
  // to resolve *last* wins regardless of which one was actually asked
  // for last, silently showing a stale page under controls that already
  // moved on. A ref, not state: aborting a superseded request is a side
  // effect on the way into a new one, never something that should
  // itself trigger a render.
  const currentFetch = useRef<AbortController | null>(null)

  // 15.D3: every (filter, page) this hook has ever fetched, in memory
  // only — a ref, not state, because writing to it must never itself
  // trigger a render; it changes on the way into a render fetchPage's
  // own setTasks/setPhase calls already schedule. Cleared, never
  // persisted: dies with the hook (a reload, a tab close), the same
  // "session-lived, not localStorage" rule docs/DECISIONS.md's
  // "Cookie httpOnly, nunca localStorage" states for the credential —
  // extended here on the same instinct, even though a task list isn't
  // itself sensitive the way a token is.
  const cacheRef = useRef<Map<string, pageCacheEntry>>(new Map())

  const fetchPage = useCallback(
    async (index: number) => {
      const cacheKey = pageCacheKey(statusFilter, priorityFilter, index)
      const cached = cacheRef.current.get(cacheKey)

      // Stale-while-revalidate: a page this hook has already fetched is
      // shown immediately, synchronously, before the network round trip
      // below even starts — this is what makes "Anterior" and revisiting
      // an already-seen filter feel instant instead of re-showing a
      // skeleton for data the hook already has. Only a genuinely new
      // (filter, page) combination falls back to the loading state.
      const hadCacheHit = Boolean(cached)
      if (cached) {
        setTasks(cached.tasks)
        setTotal(cached.total)
        setError(null)
        setPhase('loaded')
      } else {
        setPhase('loading')
      }

      currentFetch.current?.abort()
      const controller = new AbortController()
      currentFetch.current = controller

      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String(index * PAGE_SIZE),
      })
      // append, not set: the endpoint reads the parameter as a list.
      for (const status of splitFilter(statusFilter)) params.append('status', status)
      for (const priority of splitFilter(priorityFilter)) params.append('priority', priority)

      // 15.D2 interaction: revalidating a page this hook already cached
      // carries its ETag, so an unchanged page comes back as a bare
      // `304` — the cheapest possible confirmation that what was already
      // shown above is still correct. A page fetched for the first time
      // has no ETag yet and skips this header entirely.
      const headers: HeadersInit | undefined = cached?.etag
        ? { 'If-None-Match': cached.etag }
        : undefined

      let response: Response
      try {
        response = await apiFetch(`/v1/tasks?${params.toString()}`, {
          signal: controller.signal,
          headers,
        })
      } catch (err) {
        // A newer call to fetchPage already aborted this one — the
        // request that superseded it owns updating state now.
        if (controller.signal.aborted) return
        // A background revalidation of a page already shown from cache
        // failing (offline, a blip) is not a reason to tear down a
        // correct, still-displayed list — silently keep showing it
        // rather than replacing good data with an error screen over a
        // check the user never asked for. A first-time fetch with
        // nothing cached to fall back on has no such option: it
        // propagates exactly as it did before this hook tracked aborts
        // or cached anything.
        if (hadCacheHit) return
        throw err
      }
      // Belt and suspenders alongside the catch above: fetch's own
      // contract is to reject an aborted request, but checking the
      // signal directly here doesn't depend on that holding in every
      // browser/polyfill, and covers the (call to this same fetchPage,
      // now superseded) response arriving to this point in the small
      // window between the abort and the rejection.
      if (controller.signal.aborted) return

      // The cached copy already shown above (or, on the very first
      // fetch, about to be shown) is confirmed current — Response.ok is
      // false for 304 (it is only true for 2xx), so this has to be
      // checked before the failure branch below, not folded into it.
      //
      // X-Total-Count is still read here, though: the backend sets it
      // before its own ETag/If-None-Match check specifically so a 304
      // never carries a stale total (see docs/DECISIONS.md's "Total
      // real na listagem") — a task added on a different page moves the
      // total without moving this page's own rows, so this page's ETag
      // can stay identical while the count the pager shows still needs
      // to change.
      if (response.status === 304) {
        const totalHeader = response.headers.get('X-Total-Count')
        if (totalHeader !== null) {
          const revalidatedTotal = Number(totalHeader)
          setTotal(revalidatedTotal)
          if (cached) cacheRef.current.set(cacheKey, { ...cached, total: revalidatedTotal })
        }
        return
      }

      if (!response.ok) {
        // Same reasoning as the catch block above: a page already shown
        // from cache keeps showing it rather than being replaced by an
        // error state over a revalidation the user never asked for.
        if (hadCacheHit) return
        setError(await classifyError(response))
        setPhase('error')
        return
      }
      const page = (await response.json()) as Task[]
      if (controller.signal.aborted) return

      const pageTotal = Number(response.headers.get('X-Total-Count') ?? 0)

      // Deleting the last row of the last page leaves you standing on a
      // page that no longer exists. Step back rather than showing an
      // empty list with a Previous button as the only way out.
      if (page.length === 0 && index > 0) {
        setPageIndex(index - 1)
        return
      }

      setTasks(page)
      setTotal(pageTotal)
      setError(null)
      setPhase('loaded')

      cacheRef.current.set(cacheKey, {
        tasks: page,
        total: pageTotal,
        etag: response.headers.get('ETag'),
      })
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
    // No setPhase('loading') here — fetchPage itself now decides that:
    // a (filter, page) already in cacheRef is shown immediately instead
    // of flashing a skeleton for data the hook already has (15.D3).
    void fetchPage(pageIndex)
    // Unmount is the one case fetchPage's own abort-the-previous-call
    // logic can't cover on its own — there is no "next" call to do the
    // aborting. Harmless when this instead fires on a dependency change
    // right before the effect reruns: fetchPage(pageIndex) below is
    // about to open its own new AbortController regardless.
    return () => currentFetch.current?.abort()
  }, [fetchPage, pageIndex])

  // A page is in flight exactly when the fetch effect above is loading
  // one. Derived rather than tracked: as its own state it needed an
  // effect to clear it, which is the "cascading render" this codebase's
  // linter rightly complains about — and a second source of truth for
  // something `phase` already knows.
  const isPaging = phase === 'loading'

  // Derived from total (issue #237's X-Total-Count) and pageIndex,
  // never tracked as its own state — the same reasoning isPaging's own
  // comment gives, and what keeps this and totalPages from ever
  // disagreeing with total after a revalidated 304 changes it.
  const hasNextPage = (pageIndex + 1) * PAGE_SIZE < total
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const nextPage = useCallback(() => {
    if (!hasNextPage) return
    setPageIndex((index) => index + 1)
  }, [hasNextPage])

  const previousPage = useCallback(() => {
    setPageIndex((index) => Math.max(0, index - 1))
  }, [])

  // No setPhase('loading') here either, same reasoning as the load
  // effect above — and in practice this only ever has a cache entry to
  // fall back on when there is nothing to reload for: the 'error' phase
  // Retry is shown from is only reached when fetchPage found no cached
  // page for this (filter, index) in the first place.
  const reload = useCallback(() => {
    void fetchPage(pageIndex)
  }, [fetchPage, pageIndex])

  // A new task is the newest, so under created_at-ascending ordering it
  // belongs on the *last* page — which is usually not the one being
  // looked at. Splicing it into the current page would show an eleventh
  // row on a page of ten and put it in the wrong place besides, so the
  // page is re-fetched instead. (The task argument stays in the
  // signature: the caller has it, and this hook owning the decision of
  // what to do with it is the point.)
  // Every cached page — every filter, not only the current one — could
  // now be wrong: a new row shifts what belongs on every page after the
  // one it lands on, exactly the "which rows fall inside the window"
  // problem this comment already names for the current page alone. A
  // per-page invalidation would need to reason about *which* other
  // pages that shift can reach; clearing everything is what stays
  // correct without that bookkeeping — the cost is a handful of re-fetches
  // for pages nobody may even revisit, paid only for the ones that are.
  const addTaskLocally = useCallback(() => {
    cacheRef.current.clear()
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
        // The row leaving this filter's result set shifts every later
        // page the same way a delete does (see removeTaskLocally) — same
        // fix, clear everything cached under every filter.
        cacheRef.current.clear()
        void fetchPage(pageIndex)
        return
      }
      setTasks((previous) => previous.map((t) => (t.id === task.id ? task : t)))

      // Patch the cache entry too, or a later revisit of this exact page
      // would show the pre-edit version pulled straight from cacheRef —
      // stale in a way a first-time fetch of this page never was. etag
      // is deliberately cleared rather than kept: the field the caller
      // just changed (title/description/priority) means Repository's
      // Version moved server-side too, and this hook has no way to
      // compute what the new value is — Task's own Version field never
      // reaches the wire (see internal/task/task.go). Dropping the etag
      // means the next revisit's If-None-Match is skipped and gets a
      // real 200 instead of a 304 that would only be right by accident.
      const cacheKey = pageCacheKey(statusFilter, priorityFilter, pageIndex)
      const cached = cacheRef.current.get(cacheKey)
      if (cached) {
        cacheRef.current.set(cacheKey, {
          tasks: cached.tasks.map((t) => (t.id === task.id ? task : t)),
          total: cached.total,
          etag: null,
        })
      }
    },
    [fetchPage, pageIndex, statusFilter, priorityFilter],
  )

  // A delete pulls every later row one place forward, so the window
  // this page represents now holds a different set — re-fetch rather
  // than leave a nine-row page with a tenth row sitting on the next one
  // that will never be seen. The same shift reaches every later page
  // under every filter, the same reasoning addTaskLocally's own comment
  // gives for clearing the whole cache rather than just this page's
  // entry.
  const removeTaskLocally = useCallback(() => {
    cacheRef.current.clear()
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
    totalPages,
    total,
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
