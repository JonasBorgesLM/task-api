import { useCallback, useEffect, useState } from 'react'
import { API_BASE, apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import type { components } from '../../api/types'
import { Button } from '../../components/Button'
import { Skeleton } from '../../components/Skeleton'
import { Toast } from '../../components/Toast'
import styles from './AttachmentList.module.css'
import { Preview } from './Preview'
import { Upload } from './Upload'

type Attachment = components['schemas']['Attachment']

export interface AttachmentListProps {
  taskId: string
}

type Status = 'loading' | 'empty' | 'error' | 'success'

const SKELETON_ROWS = 2

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB']
  let value = bytes / 1024
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  return `${value.toFixed(1)} ${units[unitIndex]}`
}

/**
 * A task's attachments: list, upload, download, delete. Gated entirely
 * by the caller (TaskItem.tsx) on attachments_enabled — this component
 * assumes it should render and never probes for that itself (AM-2, see
 * plan.md's CI-9 entry: feature-detection is a contract concern decided
 * once via GET /auth/me, not an HTTP heuristic repeated here).
 *
 * Download is a plain <a href> to the *absolute* API_BASE origin — the
 * frontend and API are different origins by the Fase 12 dual-auth-mode
 * design, so a relative href would resolve against this app's own origin
 * instead. The httpOnly session cookie rides along automatically; no
 * fetch/object-URL dance like Preview's, because a download is never
 * rendered in this app's own document (see Content-Disposition:
 * attachment + nosniff, which is exactly what makes an <img src> to the
 * same URL not work for Preview).
 */
export function AttachmentList({ taskId }: AttachmentListProps) {
  const [status, setStatus] = useState<Status>('loading')
  const [attachments, setAttachments] = useState<Attachment[]>([])
  const [error, setError] = useState<ApiError | null>(null)
  const [deletingKey, setDeletingKey] = useState<string | null>(null)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)

  const load = useCallback(async () => {
    setStatus('loading')
    const response = await apiFetch(`/v1/tasks/${taskId}/attachments`)
    if (!response.ok) {
      setError(await classifyError(response))
      setStatus('error')
      return
    }
    const body = (await response.json()) as Attachment[]
    setAttachments(body)
    setError(null)
    setStatus(body.length === 0 ? 'empty' : 'success')
  }, [taskId])

  // oxlint's set-state-in-effect rule: fetch-on-mount is the canonical
  // "synchronize with an external system" case its own guidance carves
  // out — same accepted pattern as useTasks.tsx and useAuth.tsx.
  useEffect(() => {
    void load()
  }, [load])

  function handleUploaded(attachment: Attachment) {
    setAttachments((previous) => [...previous, attachment])
    setStatus('success')
    setSuccessMessage('File uploaded.')
  }

  async function handleDelete(storageKey: string) {
    setDeleteError(null)
    setDeletingKey(storageKey)
    try {
      const response = await apiFetch(`/v1/files/${storageKey}`, { method: 'DELETE' })
      if (!response.ok) {
        const classified = await classifyError(response)
        setDeleteError(
          classified.kind === 'not_found'
            ? 'This file is already gone.'
            : 'Could not delete this file. Please try again.',
        )
        return
      }
      const next = attachments.filter((a) => a.storage_key !== storageKey)
      setAttachments(next)
      setStatus(next.length === 0 ? 'empty' : 'success')
      setSuccessMessage('File deleted.')
    } finally {
      setDeletingKey(null)
    }
  }

  return (
    <div className={styles.container}>
      {successMessage && (
        <Toast
          message={successMessage}
          variant="success"
          onDismiss={() => setSuccessMessage(null)}
        />
      )}

      {/* Upload's own position is deliberately unconditional, never
          nested inside a status-dependent wrapper: changing its parent
          element type between renders would unmount and remount it
          (losing the hidden file input's state) every time status
          crosses into or out of 'empty' (Fase 14 CI-8's finding). The
          empty state used to add a "No attachments yet." caption here;
          removed per design review — an upload control that's just
          sitting there already reads as "nothing here yet," and the
          caption was one more line repeated across every empty task in
          a list. */}
      <div className={styles.uploadRow}>
        <Upload taskId={taskId} onUploaded={handleUploaded} />
      </div>

      {status === 'loading' && (
        <ul className={styles.list} aria-busy="true" aria-label="Loading attachments">
          {Array.from({ length: SKELETON_ROWS }, (_, i) => (
            <li key={i} className={styles.skeletonItem}>
              <Skeleton width="60%" height="1rem" />
            </li>
          ))}
        </ul>
      )}

      {status === 'error' && (
        <p className={styles.error} role="alert">
          {error?.kind === 'unavailable'
            ? "Couldn't load attachments — the service is temporarily unavailable. "
            : "Couldn't load attachments. "}
          <Button variant="secondary" onClick={() => void load()}>
            Retry
          </Button>
        </p>
      )}

      {status === 'success' && (
        <ul className={styles.list}>
          {attachments.map((attachment) => (
            <li key={attachment.id} className={styles.item}>
              {attachment.content_type.startsWith('image/') && (
                <Preview storageKey={attachment.storage_key} alt={attachment.original_filename} />
              )}
              <div className={styles.itemInfo}>
                <a
                  className={styles.filename}
                  href={`${API_BASE}/v1/files/${attachment.storage_key}`}
                  download={attachment.original_filename}
                >
                  {attachment.original_filename}
                </a>
                <span className={styles.size}>{formatBytes(attachment.size_bytes)}</span>
              </div>
              <Button
                variant="danger"
                loading={deletingKey === attachment.storage_key}
                onClick={() => void handleDelete(attachment.storage_key)}
              >
                Delete
              </Button>
            </li>
          ))}
        </ul>
      )}

      {deleteError && (
        <p className={styles.error} role="alert">
          {deleteError}
        </p>
      )}
    </div>
  )
}
