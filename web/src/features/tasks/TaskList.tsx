import { useEffect, useRef, useState } from 'react'
import { Button } from '../../components/Button'
import { Modal } from '../../components/Modal'
import { Select } from '../../components/Select'
import { Skeleton } from '../../components/Skeleton'
import { Toast } from '../../components/Toast'
import styles from './TaskList.module.css'
import { STATUS_LABELS } from './TaskStatusControls'
import { TaskForm } from './TaskForm'
import { TaskItem } from './TaskItem'
import type { Task } from './useTasks'
import { useTasks } from './useTasks'

const SKELETON_ROWS = 5

type StatusFilter = Task['status'] | ''
type PriorityFilter = Task['priority'] | ''

const PRIORITY_LABELS: Record<Task['priority'], string> = {
  low: 'Low',
  medium: 'Medium',
  high: 'High',
}

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
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('')
  const [priorityFilter, setPriorityFilter] = useState<PriorityFilter>('')
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
  } = useTasks(statusFilter, priorityFilter)
  const sentinelRef = useRef<HTMLDivElement>(null)
  const [creating, setCreating] = useState(false)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const isFiltered = statusFilter !== '' || priorityFilter !== ''

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

      <div className={styles.pageHeader}>
        <div className={styles.titleGroup}>
          <h2 className={styles.title}>Tasks</h2>
          {status === 'success' && (
            <span
              className={styles.count}
              aria-label={`${tasks.length}${hasMore ? ' or more' : ''} tasks`}
            >
              {tasks.length}
              {hasMore ? '+' : ''}
            </span>
          )}
        </div>
        <Button onClick={() => setCreating(true)}>New task</Button>
      </div>

      <div className={styles.filters}>
        <Select
          label="Status"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
        >
          <option value="">All statuses</option>
          {(Object.entries(STATUS_LABELS) as [Task['status'], string][]).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </Select>
        <Select
          label="Priority"
          value={priorityFilter}
          onChange={(e) => setPriorityFilter(e.target.value as PriorityFilter)}
        >
          <option value="">All priorities</option>
          {(Object.entries(PRIORITY_LABELS) as [Task['priority'], string][]).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </Select>
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

      {status === 'empty' && (
        <p className={styles.empty}>
          {isFiltered ? 'No tasks match this filter.' : "You don't have any tasks yet."}
        </p>
      )}

      {status === 'success' && (
        <>
          <p className={styles.note}>
            Sorted by creation date, oldest first. Filter by status or priority above — there's no
            free-text search yet.
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
