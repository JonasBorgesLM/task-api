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
    expect(result.current.hasMore).toBe(true)
  })

  it('exactly limit back (not limit+1) means there is NO more', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))

    expect(result.current.tasks).toHaveLength(PAGE_SIZE)
    expect(result.current.hasMore).toBe(false)
  })

  it('loadMore appends the next page at the correct offset and never duplicates the discarded extra item', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(PAGE_SIZE + 1, 0)))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(5, PAGE_SIZE)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.tasks).toHaveLength(PAGE_SIZE)

    await act(async () => {
      result.current.loadMore()
    })
    await waitFor(() => expect(result.current.isLoadingMore).toBe(false))

    const secondUrl = fetchMock.mock.calls[1]![0]
    expect(String(secondUrl)).toContain(`offset=${PAGE_SIZE}`)

    expect(result.current.tasks).toHaveLength(PAGE_SIZE + 5)
    expect(result.current.hasMore).toBe(false)
    // No duplicate: the (limit+1)th item discarded from page 1 is not
    // re-shown — page 2's first real item starts fresh at offset PAGE_SIZE.
    const ids = result.current.tasks.map((t) => t.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('loadMore is a no-op when hasMore is false', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, makeTasks(3)))

    const { result } = renderHook(() => useTasks())
    await waitFor(() => expect(result.current.status).toBe('success'))
    expect(result.current.hasMore).toBe(false)

    act(() => {
      result.current.loadMore()
    })

    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('reload resets to the first page', async () => {
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
})
