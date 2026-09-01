/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { Preview } from './Preview'

describe('Preview.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'Preview.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'Preview.module.css')
  })
})

describe('Preview', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-url')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('fetches the file through apiFetch (credentialed) and renders it as an <img> via an object URL', async () => {
    const fetchMock = vi.mocked(fetch)
    // Built from a plain string, not a Blob: jsdom's Blob polyfill
    // (the global this test environment provides) isn't structurally
    // compatible with Node's native Response constructor (which
    // expects its own native Blob) — confirmed by actually hitting
    // "object.stream is not a function" when a jsdom Blob was passed
    // in. A string body sidesteps the mismatch; response.blob() still
    // produces a real, usable Blob for the component to consume.
    fetchMock.mockResolvedValueOnce(
      new Response('fake-image-bytes', { status: 200, headers: { 'Content-Type': 'image/png' } }),
    )

    render(<Preview storageKey="key-1" alt="report.png" />)

    const img = await screen.findByRole('img', { name: 'report.png' })
    expect(img).toHaveAttribute('src', 'blob:mock-url')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(String(fetchMock.mock.calls[0]![0])).toContain('/v1/files/key-1')
  })

  it('revokes the object URL on unmount — the leak this component exists to prevent', async () => {
    const fetchMock = vi.mocked(fetch)
    // Built from a plain string, not a Blob: jsdom's Blob polyfill
    // (the global this test environment provides) isn't structurally
    // compatible with Node's native Response constructor (which
    // expects its own native Blob) — confirmed by actually hitting
    // "object.stream is not a function" when a jsdom Blob was passed
    // in. A string body sidesteps the mismatch; response.blob() still
    // produces a real, usable Blob for the component to consume.
    fetchMock.mockResolvedValueOnce(
      new Response('fake-image-bytes', { status: 200, headers: { 'Content-Type': 'image/png' } }),
    )

    const { unmount } = render(<Preview storageKey="key-1" alt="report.png" />)
    await screen.findByRole('img', { name: 'report.png' })
    expect(URL.revokeObjectURL).not.toHaveBeenCalled()

    unmount()

    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url')
  })

  it('does not create (or need to revoke) an object URL when the fetch fails', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 404 }))

    const { unmount, container } = render(<Preview storageKey="key-1" alt="report.png" />)
    await waitFor(() => expect(container.querySelector('img')).not.toBeInTheDocument())

    unmount()

    expect(URL.createObjectURL).not.toHaveBeenCalled()
    expect(URL.revokeObjectURL).not.toHaveBeenCalled()
  })

  it('does not leak a URL if the component unmounts before the fetch resolves', async () => {
    const fetchMock = vi.mocked(fetch)
    let resolveFetch!: (response: Response) => void
    fetchMock.mockReturnValueOnce(
      new Promise<Response>((resolve) => {
        resolveFetch = resolve
      }),
    )

    const { unmount } = render(<Preview storageKey="key-1" alt="report.png" />)
    unmount()

    resolveFetch(
      new Response('fake-image-bytes', { status: 200, headers: { 'Content-Type': 'image/png' } }),
    )
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled())

    // The fetch resolved after unmount — createObjectURL must never
    // have been called at all, so there's nothing orphaned to revoke.
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })
})
