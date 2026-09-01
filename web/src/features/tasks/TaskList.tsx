import { useEffect, useRef } from 'react'
import { Button } from '../../components/Button'
import { Skeleton } from '../../components/Skeleton'
import styles from './TaskList.module.css'
import { useTasks } from './useTasks'

const SKELETON_ROWS = 5

/**
 * Four explicit states (loading/empty/error/success) — no generic
 * spinner standing in for all of them. Pagination is infinite scroll
 * via the "ask for one extra" technique (see useTasks.tsx) — there is
 * no numbered "page N of M" anywhere here, because GET /v1/tasks gives
 * this app no total count to build one from.
 */
export function TaskList() {
  const { status, tasks, error, hasMore, isLoadingMore, loadMore, reload } = useTasks()
  const sentinelRef = useRef<HTMLDivElement>(null)

  // Infinite scroll is a progressive enhancement over the real "Load
  // more" button below, not a replacement for it: IntersectionObserver
  // has no keyboard equivalent, so a keyboard/screen-reader user relies
  // on the button either way. Feature-detected rather than assumed
  // present — this project's jsdom test environment does not implement
  // it, and this effect simply does nothing there, which is correct:
  // the button alone is what the tests exercise.
  useEffect(() => {
    if (!hasMore || typeof IntersectionObserver === 'undefined') return
    const sentinel = sentinelRef.current
    if (!sentinel) return

    const observer = new IntersectionObserver((entries) => {
      if (entries[0]?.isIntersecting) loadMore()
    })
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [hasMore, loadMore])

  if (status === 'loading') {
    return (
      <div className={styles.container}>
        <ul className={styles.list} aria-busy="true" aria-label="Loading tasks">
          {Array.from({ length: SKELETON_ROWS }, (_, i) => (
            <li key={i} className={styles.skeletonItem}>
              <div className={styles.skeletonRow}>
                <Skeleton width="60%" height="1.25rem" />
                <Skeleton width="4rem" height="1.25rem" radius="full" />
              </div>
              <Skeleton width="90%" height="0.875rem" />
            </li>
          ))}
        </ul>
      </div>
    )
  }

  if (status === 'error') {
    return (
      <div className={styles.container}>
        <p className={styles.error} role="alert">
          {error?.kind === 'unavailable'
            ? "Couldn't load your tasks — the service is temporarily unavailable. "
            : "Couldn't load your tasks. "}
          <Button variant="secondary" onClick={reload}>
            Retry
          </Button>
        </p>
      </div>
    )
  }

  if (status === 'empty') {
    return (
      <div className={styles.container}>
        <p className={styles.empty}>You don't have any tasks yet.</p>
      </div>
    )
  }

  return (
    <div className={styles.container}>
      <p className={styles.note}>
        Sorted by creation date, oldest first. There's no filtering or a different sort order yet —
        this list always shows everything, in the order the API returns it.
      </p>
      <ul className={styles.list}>
        {tasks.map((task) => (
          <li key={task.id} className={styles.item}>
            <div className={styles.itemHeader}>
              <h3 className={styles.title}>{task.title}</h3>
              <div className={styles.badges}>
                <span className={styles.badge}>{task.status}</span>
                <span className={styles.badge}>{task.priority}</span>
              </div>
            </div>
            {task.description && <p className={styles.description}>{task.description}</p>}
          </li>
        ))}
      </ul>
      {hasMore && (
        <div className={styles.loadMore} ref={sentinelRef}>
          <Button variant="secondary" onClick={loadMore} loading={isLoadingMore}>
            Load more
          </Button>
        </div>
      )}
    </div>
  )
}
