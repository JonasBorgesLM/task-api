/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { Upload } from './Upload'

vi.mock('../../api/client', () => ({ uploadFile: vi.fn() }))

// Imported after the mock so it resolves to the mocked binding — same
// ordering client.test.ts-adjacent suites in this app already rely on.
const { uploadFile } = await import('../../api/client')
const mockedUploadFile = vi.mocked(uploadFile)

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const ATTACHMENT = {
  id: 'a1',
  task_id: 't1',
  original_filename: 'report.pdf',
  storage_key: 'k1',
  content_type: 'application/pdf',
  size_bytes: 5,
  created_at: '2026-01-01T00:00:00Z',
}

describe('Upload.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'Upload.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'Upload.module.css')
  })
})

describe('Upload', () => {
  beforeEach(() => {
    mockedUploadFile.mockReset()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('uploads a selected file and reports the created attachment', async () => {
    mockedUploadFile.mockResolvedValueOnce(jsonResponse(201, ATTACHMENT))
    const onUploaded = vi.fn()
    const file = new File(['hello'], 'report.pdf', { type: 'application/pdf' })

    render(<Upload taskId="t1" onUploaded={onUploaded} />)
    const input = screen.getByLabelText('Choose a file to upload')
    await userEvent.upload(input, file)

    await waitFor(() => expect(onUploaded).toHaveBeenCalledWith(ATTACHMENT))
    expect(mockedUploadFile).toHaveBeenCalledWith(
      '/v1/tasks/t1/attachments',
      file,
      expect.any(Function),
    )
  })

  it('reports upload progress via a native <progress> element', async () => {
    let capturedOnProgress: ((p: { loaded: number; total: number }) => void) | undefined
    mockedUploadFile.mockImplementationOnce((_path, _file, onProgress) => {
      capturedOnProgress = onProgress
      return new Promise(() => {}) // intentionally never resolves — this test only cares about progress
    })
    const file = new File(['hello'], 'report.pdf', { type: 'application/pdf' })

    render(<Upload taskId="t1" onUploaded={vi.fn()} />)
    await userEvent.upload(screen.getByLabelText('Choose a file to upload'), file)

    await waitFor(() => expect(capturedOnProgress).toBeDefined())
    capturedOnProgress!({ loaded: 2, total: 5 })

    const progress = await screen.findByRole('progressbar', { name: 'Upload progress' })
    await waitFor(() => expect((progress as HTMLProgressElement).value).toBe(2))
  })

  it('shows a distinct message for a content-type-rejected 400', async () => {
    mockedUploadFile.mockResolvedValueOnce(
      jsonResponse(400, { error: 'invalid input: content type "application/zip" is not accepted' }),
    )
    const file = new File(['hello'], 'report.pdf', { type: 'application/pdf' })

    render(<Upload taskId="t1" onUploaded={vi.fn()} />)
    await userEvent.upload(screen.getByLabelText('Choose a file to upload'), file)

    expect(await screen.findByRole('alert')).toHaveTextContent("That file type isn't supported.")
  })

  it('shows a distinct message for a quota-exceeded 400 — same status code as content-type rejection', async () => {
    mockedUploadFile.mockResolvedValueOnce(
      jsonResponse(400, {
        error: 'invalid input: attachment quota exceeded (524288000/524288000 bytes used)',
      }),
    )
    const file = new File(['hello'], 'report.pdf', { type: 'application/pdf' })

    render(<Upload taskId="t1" onUploaded={vi.fn()} />)
    await userEvent.upload(screen.getByLabelText('Choose a file to upload'), file)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'You have used up your attachment storage quota.',
    )
  })

  it('rejects an oversized file client-side without calling uploadFile', async () => {
    const bigFile = new File([new Uint8Array(1)], 'big.pdf', { type: 'application/pdf' })
    Object.defineProperty(bigFile, 'size', { value: 11 * 1024 * 1024 })

    render(<Upload taskId="t1" onUploaded={vi.fn()} />)
    await userEvent.upload(screen.getByLabelText('Choose a file to upload'), bigFile)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'larger than the 10 MiB per-file limit',
    )
    expect(mockedUploadFile).not.toHaveBeenCalled()
  })

  it('rejects an unsupported file type client-side without calling uploadFile', async () => {
    const file = new File(['x'], 'archive.zip', { type: 'application/zip' })

    render(<Upload taskId="t1" onUploaded={vi.fn()} />)
    await userEvent.upload(screen.getByLabelText('Choose a file to upload'), file)

    expect(await screen.findByRole('alert')).toHaveTextContent("isn't supported")
    expect(mockedUploadFile).not.toHaveBeenCalled()
  })
})
