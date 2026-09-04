import { useState } from 'react'
import { Button } from '../../components/Button'
import {
  ChevronLeftIcon,
  ChevronRightIcon,
  FilterIcon,
  PlusIcon,
  RefreshIcon,
} from '../../components/icons'
import type { MenuItem } from '../../components/Menu'
import { Menu } from '../../components/Menu'
import { Modal } from '../../components/Modal'
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

const PRIORITY_LABELS: Record<Task['priority'], string> = {
  low: 'Low',
  medium: 'Medium',
  high: 'High',
}

const ALL_STATUSES: Task['status'][] = ['pending', 'in_progress', 'done', 'cancelled']
const ALL_PRIORITIES: Task['priority'][] = ['high', 'medium', 'low']

// Cancelled is off to begin with: a cancelled task is one you decided
// not to do, and having it in the default view means every list is
// partly made of things nobody is going to work on. It is one click
// away, and the filter says so when it is on.
const DEFAULT_STATUSES: Task['status'][] = ['pending', 'in_progress', 'done']

/**
 * "Everything is selected" and "nothing is selected" would send the same
 * request — the API reads an absent filter as "no filter" — so the
 * label has to distinguish them for the user even though the wire
 * cannot. Naming one or two selections outright beats a count: "Pending"
 * says more than "1 status".
 *
 * The plural is passed in rather than derived by adding an "s": neither
 * of the two nouns this is called with forms its plural that way, and
 * the first version of this shipped "statuss" on screen.
 */
function filterLabel(
  selected: string[],
  all: string[],
  labels: Record<string, string>,
  plural: string,
) {
  if (selected.length === all.length) return `All ${plural}`
  if (selected.length <= 2) return selected.map((value) => labels[value]).join(', ')
  return `${selected.length} ${plural}`
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
  const [statuses, setStatuses] = useState<Task['status'][]>(DEFAULT_STATUSES)
  const [priorities, setPriorities] = useState<Task['priority'][]>(ALL_PRIORITIES)
  // Joined here rather than inside useTasks: the hook needs a primitive
  // to compare, and this is the one place that knows the order.
  const statusFilter = statuses.length === ALL_STATUSES.length ? '' : statuses.join(',')
  const priorityFilter = priorities.length === ALL_PRIORITIES.length ? '' : priorities.join(',')
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
  // "Narrowed by the user", not "any filter is active": the default
  // itself excludes cancelled, so a plain `statusFilter !== ''` would
  // greet someone with no tasks at all with "no tasks match this
  // filter" — technically true, unhelpful, and not what they did.
  const isNarrowed =
    statuses.length !== DEFAULT_STATUSES.length ||
    !DEFAULT_STATUSES.every((status) => statuses.includes(status)) ||
    priorities.length !== ALL_PRIORITIES.length

  // Toggling off the last remaining value would ask for nothing at all,
  // which the API cannot express — an absent filter means "no filter",
  // so an empty selection would silently return everything, the exact
  // opposite of what unticking the last box looks like it should do.
  // The last one selected therefore stays selected.
  function toggle<T extends string>(values: T[], value: T, setValues: (next: T[]) => void) {
    const next = values.includes(value) ? values.filter((v) => v !== value) : [...values, value]
    if (next.length === 0) return
    setValues(next)
  }

  const statusItems: MenuItem[] = ALL_STATUSES.map((status) => ({
    key: status,
    label: STATUS_LABELS[status],
    selected: statuses.includes(status),
    onSelect: () => toggle(statuses, status, setStatuses),
  }))

  const priorityItems: MenuItem[] = ALL_PRIORITIES.map((priority) => ({
    key: priority,
    label: PRIORITY_LABELS[priority],
    selected: priorities.includes(priority),
    onSelect: () => toggle(priorities, priority, setPriorities),
  }))

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
          <Menu
            multi
            triggerLabel="Filter by status"
            triggerIcon={<FilterIcon />}
            triggerText={filterLabel(statuses, ALL_STATUSES, STATUS_LABELS, 'statuses')}
            items={statusItems}
          />
          <Menu
            multi
            triggerLabel="Filter by priority"
            triggerIcon={<FilterIcon />}
            triggerText={filterLabel(priorities, ALL_PRIORITIES, PRIORITY_LABELS, 'priorities')}
            items={priorityItems}
          />
        </div>

        <TaskStats tasks={tasks} isFiltered={isNarrowed} />

        <Button onClick={() => setCreating(true)}>
          <PlusIcon />
          New task
        </Button>
      </div>
      <Modal open={creating} onClose={() => setCreating(false)} title="New task">
        <TaskForm onCancel={() => setCreating(false)} onSuccess={handleCreated} />
      </Modal>

      {/* The header stays mounted through a load, and only the list
          below it swaps for skeletons. Returning early on 'loading'
          unmounted the filters on every fetch — which threw away the
          open filter menu the moment you ticked something in it, since
          ticking triggers the fetch that unmounted it. */}
      {status === 'loading' && (
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
      )}

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
          {isNarrowed
            ? 'No tasks match this filter.'
            : // Says both things it can honestly say: there is nothing
              // here, and the default view is not showing everything.
              "You don't have any tasks yet. Cancelled ones are hidden by default."}
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
