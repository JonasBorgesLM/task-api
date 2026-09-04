import { useState } from 'react'
import { Button } from '../../components/Button'
import { ChevronLeftIcon, ChevronRightIcon, PlusIcon, RefreshIcon } from '../../components/icons'
import { Modal } from '../../components/Modal'
import { Select } from '../../components/Select'
import { Skeleton } from '../../components/Skeleton'
import { Toast } from '../../components/Toast'
import styles from './TaskList.module.css'
import { STATUS_LABELS } from './TaskStatusControls'
import { TaskForm } from './TaskForm'
import { TaskItem } from './TaskItem'
import { TaskStats } from './TaskStats'
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
    page,
    hasNextPage,
    hasPreviousPage,
    isPaging,
    nextPage,
    previousPage,
    reload,
    addTaskLocally,
    updateTaskLocally,
    removeTaskLocally,
  } = useTasks(statusFilter, priorityFilter)
  const [creating, setCreating] = useState(false)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const isFiltered = statusFilter !== '' || priorityFilter !== ''

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

      {/* One line: the two filters, the counts button, then create. The
          page title and its running count used to sit here; the title
          repeated the only thing this screen shows, and the count is
          now one glyph away instead of always on screen (design
          review). Neither filter carries a visible caption —
          "All statuses"/"All priorities" already say what they do. */}
      <div className={styles.pageHeader}>
        <div className={styles.filters}>
          <Select
            label="Status"
            labelHidden
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
            labelHidden
            value={priorityFilter}
            onChange={(e) => setPriorityFilter(e.target.value as PriorityFilter)}
          >
            <option value="">All priorities</option>
            {(Object.entries(PRIORITY_LABELS) as [Task['priority'], string][]).map(
              ([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ),
            )}
          </Select>
        </div>

        <TaskStats tasks={tasks} isFiltered={isFiltered} />

        <Button onClick={() => setCreating(true)}>
          <PlusIcon />
          New task
        </Button>
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
            <RefreshIcon />
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

          {/* A page number and two directions, and nothing the API can't
              tell this client: GET /v1/tasks returns no total, so there
              is no "of 12" to render and no last-page jump to offer.
              Next is enabled only when the extra row this page asked
              for actually came back — see useTasks. */}
          {(hasPreviousPage || hasNextPage) && (
            <nav className={styles.pager} aria-label="Task pages">
              <Button
                variant="secondary"
                onClick={previousPage}
                disabled={!hasPreviousPage || isPaging}
              >
                <ChevronLeftIcon />
                Previous
              </Button>
              <span className={styles.pageNumber} aria-live="polite">
                Page {page}
              </span>
              <Button variant="secondary" onClick={nextPage} disabled={!hasNextPage || isPaging}>
                Next
                <ChevronRightIcon />
              </Button>
            </nav>
          )}
        </>
      )}
    </div>
  )
}
