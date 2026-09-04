import { useRef, useState } from 'react'
import type { UploadProgress } from '../../api/client'
import { uploadFile } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import type { components } from '../../api/types'
import { Button } from '../../components/Button'
import { PaperclipIcon } from '../../components/icons'
import styles from './Upload.module.css'

type Attachment = components['schemas']['Attachment']

export interface UploadProps {
  taskId: string
  onUploaded: (attachment: Attachment) => void
}

// Mirrors internal/config/config.go's defaultAttachmentMaxBytes — checked
// here only to save a doomed round trip for the common case. The server
// (ATTACHMENT_MAX_BYTES) is the sole authority: a deployment that raises
// the env var still accepts a file this client would reject, and that's
// fine, since a real 400 is still handled below.
const MAX_FILE_BYTES = 10 * 1024 * 1024

// Mirrors internal/attachment/service.go's allowedContentTypes. Guidance
// only — the server decides by sniffing the bytes
// (http.DetectContentType), never by the type a client declares. Checked
// here purely so an obviously-wrong file (e.g. a .zip) gets an immediate,
// specific message instead of a round trip to learn the same thing; a
// file this check accepts can still come back rejected, since a
// browser-declared type and the server's sniffed one can disagree.
const ACCEPTED_CONTENT_TYPES = new Set([
  'image/jpeg',
  'image/png',
  'image/gif',
  'image/webp',
  'application/pdf',
  'text/plain',
])

// The 400 for a rejected content type and the 400 for an exceeded quota
// are the same status code (see docs/openapi.yaml's uploadAttachment
// 400) — distinguished only by matching the message text against
// internal/attachment/service.go's two fmt.Errorf calls, the same
// message-substring technique errors.ts's classifyConflict already uses
// for the two 409 reasons on PATCH /status (AM-5).
function messageForUploadError(error: ApiError): string {
  if (error.kind === 'invalid_input') {
    if (error.message.includes('quota exceeded')) {
      return 'You have used up your attachment storage quota.'
    }
    if (error.message.includes('is not accepted')) {
      return "That file type isn't supported."
    }
    return error.message
  }
  switch (error.kind) {
    case 'not_found':
      return 'This task is gone.'
    case 'rate_limited':
      return 'Too many attempts. Please wait a moment and try again.'
    default:
      return 'Upload failed. Please try again.'
  }
}

/**
 * Multipart upload with real progress. Delegates the actual HTTP call to
 * client.ts's uploadFile — the one request in this app that doesn't go
 * through apiFetch, because fetch has no broadly-supported upload
 * (request body) progress API. The file <input> stays in the document
 * (hidden, not display:none — a hidden-but-present input is what keeps
 * inputRef.current.click() working) and is triggered by a normal Button,
 * so the visible control is this app's own styled primitive rather than
 * the browser's unstylable native file picker button.
 */
export function Upload({ taskId, onUploaded }: UploadProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [status, setStatus] = useState<'idle' | 'uploading' | 'error'>('idle')
  const [progress, setProgress] = useState<UploadProgress | null>(null)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)

  async function handleFile(file: File) {
    setErrorMessage(null)

    if (file.size > MAX_FILE_BYTES) {
      setStatus('error')
      setErrorMessage('This file is larger than the 10 MiB per-file limit.')
      return
    }
    if (file.type && !ACCEPTED_CONTENT_TYPES.has(file.type)) {
      setStatus('error')
      setErrorMessage("That file type isn't supported.")
      return
    }

    setStatus('uploading')
    setProgress({ loaded: 0, total: file.size })
    try {
      const response = await uploadFile(`/v1/tasks/${taskId}/attachments`, file, setProgress)
      if (!response.ok) {
        setStatus('error')
        setErrorMessage(messageForUploadError(await classifyError(response)))
        return
      }
      setStatus('idle')
      setProgress(null)
      onUploaded((await response.json()) as Attachment)
    } catch {
      setStatus('error')
      setErrorMessage('Upload failed. Please try again.')
    } finally {
      // Clears the selection so choosing the exact same file again still
      // fires a change event next time — the browser doesn't fire one
      // for an unchanged value.
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  return (
    <div className={styles.container}>
      <input
        ref={inputRef}
        type="file"
        tabIndex={-1}
        className={styles.hiddenInput}
        aria-label="Choose a file to upload"
        onChange={(event) => {
          const file = event.target.files?.[0]
          if (file) void handleFile(file)
        }}
      />
      <Button
        type="button"
        variant="secondary"
        loading={status === 'uploading'}
        onClick={() => inputRef.current?.click()}
      >
        <PaperclipIcon />
        Upload file
      </Button>

      {status === 'uploading' && progress && (
        <progress
          className={styles.progress}
          value={progress.loaded}
          max={progress.total}
          aria-label="Upload progress"
        />
      )}

      {status === 'error' && errorMessage && (
        <p className={styles.error} role="alert">
          {errorMessage}
        </p>
      )}
    </div>
  )
}
