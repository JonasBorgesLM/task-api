import { useEffect, useState } from 'react'
import { apiFetch } from '../../api/client'
import { Skeleton } from '../../components/Skeleton'
import styles from './Preview.module.css'

export interface PreviewProps {
  storageKey: string
  alt: string
}

/**
 * Image thumbnail — never a plain <img src="/v1/files/{key}">. The
 * download response carries Content-Disposition: attachment and
 * X-Content-Type-Options: nosniff (see docs/openapi.yaml and
 * CLAUDE.md's attachment rules), specifically so a browser downloads
 * user-supplied bytes instead of rendering them in this app's origin —
 * an <img src> pointed straight at that URL would trigger a download,
 * not a picture. A thumbnail needs the bytes fetched through
 * credentialed apiFetch and turned into an object URL instead.
 *
 * The object URL is revoked on unmount without exception — an
 * un-revoked one keeps its backing Blob alive in memory for the life of
 * the tab, which for a list of image attachments would leak steadily
 * worse the more of them a caller scrolls past.
 */
export function Preview({ storageKey, alt }: PreviewProps) {
  const [objectUrl, setObjectUrl] = useState<string | null>(null)
  const [status, setStatus] = useState<'loading' | 'loaded' | 'error'>('loading')

  useEffect(() => {
    let cancelled = false
    let createdUrl: string | null = null

    apiFetch(`/v1/files/${storageKey}`).then(async (response) => {
      if (!response.ok) {
        if (!cancelled) setStatus('error')
        return
      }
      const blob = await response.blob()
      // Unmounted while the fetch/blob was in flight — don't create a
      // URL nothing will ever revoke, and don't set state on an
      // unmounted component.
      if (cancelled) return
      createdUrl = URL.createObjectURL(blob)
      setObjectUrl(createdUrl)
      setStatus('loaded')
    })

    return () => {
      cancelled = true
      if (createdUrl) URL.revokeObjectURL(createdUrl)
    }
  }, [storageKey])

  if (status === 'loading') {
    return <Skeleton width="4rem" height="4rem" />
  }

  // Silent fallback, not an error banner: a broken thumbnail for one
  // attachment in a list isn't worth the same weight as TaskList's own
  // full-list error state — the filename and download link elsewhere in
  // the row still work regardless.
  if (status === 'error' || !objectUrl) {
    return null
  }

  return <img src={objectUrl} alt={alt} className={styles.thumbnail} />
}
