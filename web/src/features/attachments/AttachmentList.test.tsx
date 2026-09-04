/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateCsrfToken } from '../../api/client'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { AttachmentList } from './AttachmentList'

// Only uploadFile is mocked — apiFetch/API_BASE stay real so list/delete
// still exercise the actual credentialed-fetch + CSRF path against the
// stubbed global fetch below, the same way every other feature test in
// this app does. Upload.test.tsx already covers the upload widget itself
// in depth; this file only needs to prove AttachmentList wires its
// onUploaded callback correctly.
vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return { ...actual, uploadFile: vi.fn() }
})

const { uploadFile } = await import('../../api/client')
const mockedUploadFile = vi.mocked(uploadFile)

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const CSRF_RESPONSE = () => jsonResponse(200, { csrf_token: 'test-csrf-token' })

const PDF_ATTACHMENT = {
  id: 'a1',
  task_id: 't1',
  original_filename: 'report.pdf',
  storage_key: 'key-pdf',
  content_type: 'application/pdf',
  size_bytes: 2048,
  created_at: '2026-01-01T00:00:00Z',
}

const IMAGE_ATTACHMENT = {
  id: 'a2',
  task_id: 't1',
  original_filename: 'photo.png',
  storage_key: 'key-img',
  content_type: 'image/png',
  size_bytes: 4096,
  created_at: '2026-01-02T00:00:00Z',
}

describe('AttachmentList.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'AttachmentList.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'AttachmentList.module.css')
  })
})

describe('AttachmentList', () => {
  beforeEach(() => {
    invalidateCsrfToken()
    mockedUploadFile.mockReset()
    vi.stubGlobal('fetch', vi.fn())
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-url')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('lists attachments with a download link to the absolute API origin', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [PDF_ATTACHMENT]))

    render(<AttachmentList taskId="t1" />)

    const link = await screen.findByRole('link', { name: 'report.pdf' })
    expect(link).toHaveAttribute('href', 'http://localhost:8080/v1/files/key-pdf')
    expect(link).toHaveAttribute('download', 'report.pdf')
    expect(screen.getByText('2.0 KB')).toBeInTheDocument()
  })

  // The empty state carries no caption of its own any more (removed in
  // the design-review pass — the bare Upload control already reads as
  // "nothing here yet"), so "empty" is asserted structurally: Upload is
  // present, and no attachment row — each of which renders a download
  // link — exists.
  it('shows the empty state when the task has no attachments', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))

    render(<AttachmentList taskId="t1" />)

    expect(await screen.findByRole('button', { name: 'Upload file' })).toBeInTheDocument()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows an error state with a working retry on a failed list request', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(
      jsonResponse(503, { error: 'service temporarily unavailable, please retry' }),
    )
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [PDF_ATTACHMENT]))

    render(<AttachmentList taskId="t1" />)

    await screen.findByRole('alert')
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByRole('link', { name: 'report.pdf' })).toBeInTheDocument()
  })

  it('renders a Preview thumbnail only for an image attachment, not a pdf', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [PDF_ATTACHMENT, IMAGE_ATTACHMENT]))
    // Preview's own fetch for the image bytes.
    fetchMock.mockResolvedValueOnce(
      new Response('fake-image-bytes', { status: 200, headers: { 'Content-Type': 'image/png' } }),
    )

    render(<AttachmentList taskId="t1" />)

    await screen.findByRole('link', { name: 'report.pdf' })
    expect(await screen.findByRole('img', { name: 'photo.png' })).toBeInTheDocument()

    const pdfRow = screen.getByRole('link', { name: 'report.pdf' }).closest('li')!
    expect(within(pdfRow).queryByRole('img')).not.toBeInTheDocument()
  })

  it('deletes an attachment and removes it from the list', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [PDF_ATTACHMENT]))
    fetchMock.mockResolvedValueOnce(CSRF_RESPONSE()) // apiFetch's CSRF fetch for the DELETE
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))

    render(<AttachmentList taskId="t1" />)
    await screen.findByRole('link', { name: 'report.pdf' })

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))

    await waitFor(() =>
      expect(screen.queryByRole('link', { name: 'report.pdf' })).not.toBeInTheDocument(),
    )
    expect(screen.getByRole('button', { name: 'Upload file' })).toBeInTheDocument()
    const deleteCall = fetchMock.mock.calls[2]!
    expect(String(deleteCall[0])).toContain('/v1/files/key-pdf')
    expect((deleteCall[1] as RequestInit).method).toBe('DELETE')
  })

  it('shows an inline error and keeps the item when delete fails', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [PDF_ATTACHMENT]))
    fetchMock.mockResolvedValueOnce(CSRF_RESPONSE())
    fetchMock.mockResolvedValueOnce(jsonResponse(404, { error: 'attachment not found' }))

    render(<AttachmentList taskId="t1" />)
    await screen.findByRole('link', { name: 'report.pdf' })

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))

    expect(await screen.findByText('This file is already gone.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'report.pdf' })).toBeInTheDocument()
  })

  it("adds an uploaded file to the list via Upload's onUploaded callback", async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    mockedUploadFile.mockResolvedValueOnce(jsonResponse(201, PDF_ATTACHMENT))

    render(<AttachmentList taskId="t1" />)
    await screen.findByRole('button', { name: 'Upload file' })

    const file = new File(['hello'], 'report.pdf', { type: 'application/pdf' })
    await userEvent.upload(screen.getByLabelText('Choose a file to upload'), file)

    expect(await screen.findByRole('link', { name: 'report.pdf' })).toBeInTheDocument()
    expect(screen.getByText('File uploaded.')).toBeInTheDocument()
  })
})
