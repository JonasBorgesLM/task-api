import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { PAGE_SIZE, useTasks } from './useTasks'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function makeTask(id: string) {
  return {
    id,
    title: `Task ${id}`,
    description: '',
    status: 'pending' as const,
    priority: 'medium' as const,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

function makeTasks(count: number, offset = 0) {
  return Array.from({ length: count }, (_, i) => makeTask(String(offset + i + 1)))
}

describe('useTasks', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('starts in "loading", then moves to "success" with the fetched tasks', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(3)))

    const { result } = renderHook(() => useTasks())

    expect(result.current.status).toBe('loading')
    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.tasks).toHaveLength(3)
  })

  it('moves to "empty" when the caller has no tasks', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))

    const { result } = renderHook(() => useTasks())

    await waitFor(() => expect(result.current.status).toBe('empty'))
    expect(result.current.tasks).toHaveLength(0)
  })

  it('moves to "error" when the request fails, and exposes a classified error', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(
      jsonResponse(503, { error: 'service temporarily unavailable, please retry' }),
    )

    const { result } = renderHook(() => useTasks())

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toEqual({
      requestId: null,
      kind: 'unavailable',
      message: 'service temporarily unavailable, please retry',
    })
  })

  it('requests limit+1: exactly limit+1 back means there IS more, and the extra item is never shown', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE + 1)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    const requestedUrl = fetchMock.mock.calls[0]![0]
    expect(String(requestedUrl)).toContain(`limit=${PAGE_SIZE + 1}`)
    expect(String(requestedUrl)).toContain('offset=0')

    expect(result.current.tasks).toHaveLength(PAGE_SIZE)
    expect(result.current.hasNextPage).toBe(true)
    expect(result.current.page).toBe(1)
    expect(result.current.hasPreviousPage).toBe(false)
  })

  it('exactly limit back (not limit+1) means there is NO more', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    expect(result.current.tasks).toHaveLength(PAGE_SIZE)
    expect(result.current.hasNextPage).toBe(false)
  })

  it('nextPage replaces the page at the next offset — it does not accumulate', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE + 1, 0)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(5, PAGE_SIZE)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.tasks).toHaveLength(PAGE_SIZE)

    await act(async () => {
      result.current.nextPage()
    })
    await waitFor(() => expect(result.current.page).toBe(2))
    await waitFor(() => expect(result.current.isPaging).toBe(false))

    const secondUrl = fetchMock.mock.calls[1]![0]
    expect(String(secondUrl)).toContain(`offset=${PAGE_SIZE}`)

    // Only page 2 — the previous page's rows are gone, which is the
    // whole point of paging rather than accumulating.
    expect(result.current.tasks).toHaveLength(5)
    expect(result.current.tasks[0]!.id).toBe(makeTasks(1, PAGE_SIZE)[0]!.id)
    expect(result.current.hasNextPage).toBe(false)
    expect(result.current.hasPreviousPage).toBe(true)
  })

  it('previousPage goes back to the page before it', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE + 1, 0)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(5, PAGE_SIZE)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE + 1, 0)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    await act(async () => {
      result.current.nextPage()
    })
    await waitFor(() => expect(result.current.page).toBe(2))

    await act(async () => {
      result.current.previousPage()
    })
    await waitFor(() => expect(result.current.page).toBe(1))

    expect(String(fetchMock.mock.calls[2]![0])).toContain('offset=0')
    expect(result.current.hasPreviousPage).toBe(false)
  })

  it('nextPage is a no-op on the last page', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(3)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.hasNextPage).toBe(false)

    act(() => {
      result.current.nextPage()
    })

    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('emptying the last page by deleting its last row steps back a page', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE + 1, 0)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(1, PAGE_SIZE)))
    // The delete's re-fetch of page 2 finds it empty...
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    // ...so the hook drops back to page 1 and fetches that instead.
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE, 0)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    await act(async () => {
      result.current.nextPage()
    })
    await waitFor(() => expect(result.current.page).toBe(2))

    await act(async () => {
      result.current.removeTaskLocally('t-10')
    })

    await waitFor(() => expect(result.current.page).toBe(1))
    expect(result.current.tasks).toHaveLength(PAGE_SIZE)
  })

  it('reload re-fetches the page being viewed', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(3)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(1)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.tasks).toHaveLength(3)

    act(() => {
      result.current.reload()
    })

    await waitFor(() => expect(result.current.tasks).toHaveLength(1))
    const secondUrl = fetchMock.mock.calls[1]![0]
    expect(String(secondUrl)).toContain('offset=0')
  })

  // A create re-fetches rather than splicing: under paging the new task
  // belongs on the *last* page, which is usually not the one on screen,
  // and an eleventh row on a page of ten would be wrong twice over.
  it('addTaskLocally re-fetches the page being viewed', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(2)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(3)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    await act(async () => {
      result.current.addTaskLocally(makeTask('new'))
    })

    await waitFor(() => expect(result.current.tasks).toHaveLength(3))
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(String(fetchMock.mock.calls[1]![0])).toContain('offset=0')
  })

  it('updateTaskLocally replaces the matching task in place, without a re-fetch', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(2)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    act(() => {
      result.current.updateTaskLocally({ ...makeTask('1'), title: 'Renamed' })
    })

    expect(result.current.tasks.map((t) => t.id)).toEqual(['1', '2'])
    expect(result.current.tasks[0]!.title).toBe('Renamed')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  // Regression: cancelling a task while cancelled is filtered out left
  // it sitting on screen, because an edit patched the row in place
  // without asking whether it still belonged in the result.
  it('updateTaskLocally re-fetches when the edit drops the task out of the active filter', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(2)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(1)))

    const { result } = renderHook(() => useTasks('pending,in_progress,done'))
    await waitFor(() => expect(result.current.status).toBe('success'))

    await act(async () => {
      result.current.updateTaskLocally({ ...makeTask('1'), status: 'cancelled' })
    })

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(result.current.tasks).toHaveLength(1)
  })

  it('updateTaskLocally still patches in place when the task keeps matching', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(2)))

    const { result } = renderHook(() => useTasks('pending,in_progress,done'))
    await waitFor(() => expect(result.current.status).toBe('success'))

    act(() => {
      result.current.updateTaskLocally({ ...makeTask('1'), title: 'Renamed', status: 'done' })
    })

    expect(result.current.tasks[0]!.title).toBe('Renamed')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  // Likewise a delete: it pulls every later row one place forward, so
  // the window this page represents now holds a different set.
  it('removeTaskLocally re-fetches the page being viewed', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(2)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(1)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    await act(async () => {
      result.current.removeTaskLocally('1')
    })

    await waitFor(() => expect(result.current.tasks).toHaveLength(1))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  // Deleting the only task on page 1 lands on the 'empty' state, the
  // same as if the server had never had one — TaskList must not need a
  // separate "just deleted the last one" state.
  it('deleting the last task on the first page ends in the empty state', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(1)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    await act(async () => {
      result.current.removeTaskLocally('1')
    })

    await waitFor(() => expect(result.current.status).toBe('empty'))
    expect(result.current.page).toBe(1)
  })

  it('omits status/priority from the request when both are the default empty string', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(1)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    const requestedUrl = String(fetchMock.mock.calls[0]![0])
    expect(requestedUrl).not.toContain('status=')
    expect(requestedUrl).not.toContain('priority=')
  })

  it('includes status/priority in the request when non-empty', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(1)))

    const { result } = renderHook(() => useTasks('pending', 'high'))
    await waitFor(() => expect(result.current.status).toBe('success'))

    const requestedUrl = String(fetchMock.mock.calls[0]![0])
    expect(requestedUrl).toContain('status=pending')
    expect(requestedUrl).toContain('priority=high')
  })

  // A filter changes what "page 1" even means, so paging back to it is
  // the only honest thing to do — staying on page 3 of the old result
  // set would show page 3 of a different one.
  it('changing a filter returns to page 1 and re-fetches from offset 0', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE + 1, 0)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(5, PAGE_SIZE)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(2)))

    const { result, rerender } = renderHook(({ status }: { status: string }) => useTasks(status), {
      initialProps: { status: '' },
    })
    await waitFor(() => expect(result.current.status).toBe('success'))

    await act(async () => {
      result.current.nextPage()
    })
    await waitFor(() => expect(result.current.page).toBe(2))

    rerender({ status: 'done' })

    await waitFor(() => expect(result.current.tasks).toHaveLength(2))
    expect(result.current.page).toBe(1)
    const thirdUrl = String(fetchMock.mock.calls[2]![0])
    expect(thirdUrl).toContain('offset=0')
    expect(thirdUrl).toContain('status=done')
  })
})
