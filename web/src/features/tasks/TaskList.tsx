import { useEffect, useRef, useState } from 'react'
import { Button } from '../../components/Button'
import { Modal } from '../../components/Modal'
import { Skeleton } from '../../components/Skeleton'
import { Toast } from '../../components/Toast'
import styles from './TaskList.module.css'
import { TaskForm } from './TaskForm'
import { TaskItem } from './TaskItem'
import type { Task } from './useTasks'
import { useTasks } from './useTasks'

const SKELETON_ROWS = 5

/**
 * Four explicit states (loading/empty/error/success) — no generic
 * spinner standing in for all of them. Pagination is infinite scroll
 * via the "ask for one extra" technique (see useTasks.tsx) — there is
 * no numbered "page N of M" anywhere here, because GET /v1/tasks gives
 * this app no total count to build one from.
 *
 * CI-8 adds create/edit/delete/status-change (TaskForm,
 * TaskStatusControls, TaskItem) and a single success-toast surface —
 * issue #126's own added criterion, "feedback de sucesso em toda
 * mutação" — shared across every mutation rather than each one
 * building its own.
 */
export function TaskList() {
  const {
    status,
    tasks,
    error,
    hasMore,
    isLoadingMore,
    loadMore,
    reload,
    addTaskLocally,
    updateTaskLocally,
    removeTaskLocally,
  } = useTasks()
  const sentinelRef = useRef<HTMLDivElement>(null)
  const [creating, setCreating] = useState(false)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)

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

  function handleCreated(task: Task) {
    setCreating(false)
    addTaskLocally(task)
    setSuccessMessage('Task created.')
  }

  function handleUpdated(task: Task) {
    updateTaskLocally(task)
    setSuccessMessage('Task updated.')
  }

  function handleDeleted(id: string) {
    removeTaskLocally(id)
    setSuccessMessage('Task deleted.')
  }

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

  return (
    <div className={styles.container}>
      {successMessage && (
        <Toast
          message={successMessage}
          variant="success"
          onDismiss={() => setSuccessMessage(null)}
        />
      )}

      <div className={styles.header}>
        <Button onClick={() => setCreating(true)}>New task</Button>
      </div>
      <Modal open={creating} onClose={() => setCreating(false)} title="New task">
        <TaskForm onCancel={() => setCreating(false)} onSuccess={handleCreated} />
      </Modal>

      {status === 'error' && (
        <p className={styles.error} role="alert">
          {error?.kind === 'unavailable'
            ? "Couldn't load your tasks — the service is temporarily unavailable. "
            : "Couldn't load your tasks. "}
          <Button variant="secondary" onClick={reload}>
            Retry
          </Button>
        </p>
      )}

      {status === 'empty' && <p className={styles.empty}>You don't have any tasks yet.</p>}

      {status === 'success' && (
        <>
          <p className={styles.note}>
            Sorted by creation date, oldest first. There's no filtering or a different sort order
            yet — this list always shows everything, in the order the API returns it.
          </p>
          <ul className={styles.list}>
            {tasks.map((task) => (
              <TaskItem
                key={task.id}
                task={task}
                onUpdated={handleUpdated}
                onDeleted={handleDeleted}
              />
            ))}
          </ul>
          {hasMore && (
            <div className={styles.loadMore} ref={sentinelRef}>
              <Button variant="secondary" onClick={loadMore} loading={isLoadingMore}>
                Load more
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
