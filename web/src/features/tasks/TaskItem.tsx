import { useState } from 'react'
import { apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import { Button } from '../../components/Button'
import { PencilIcon, TrashIcon } from '../../components/icons'
import { Mark } from '../../components/Mark'
import { Modal } from '../../components/Modal'
import { AttachmentList } from '../attachments/AttachmentList'
import { useAuth } from '../auth/useAuth'
import styles from './TaskItem.module.css'
import { TaskForm } from './TaskForm'
import { TaskStatusControls } from './TaskStatusControls'
import type { Task } from './useTasks'

export interface TaskItemProps {
  task: Task
  onUpdated: (task: Task) => void
  onDeleted: (id: string) => void
}

// CSS module imports are index-signature typed, so noUncheckedIndexedAccess
// (a deliberate floor — see tsconfig.app.json) marks every property access
// string | undefined even for a class known to exist. `?? ''` at the call
// site handles that structurally instead of asserting it away.
const STATUS_BADGE_CLASS: Record<Task['status'], string | undefined> = {
  pending: styles.statusPending,
  in_progress: styles.statusInProgress,
  done: styles.statusDone,
  cancelled: styles.statusCancelled,
}

const PRIORITY_BADGE_CLASS: Record<Task['priority'], string | undefined> = {
  low: styles.priorityLow,
  medium: styles.priorityMedium,
  high: styles.priorityHigh,
}

// The card's own left edge, tinted by priority — the scan-a-list-fast
// pattern Trello/ClickUp/monday all use. Same three tokens the priority
// badge already uses, so the stripe and the pill can never disagree.
const PRIORITY_STRIPE_CLASS: Record<Task['priority'], string | undefined> = {
  low: styles.stripeLow,
  medium: styles.stripeMedium,
  high: styles.stripeHigh,
}

function messageForDeleteError(error: ApiError): string {
  switch (error.kind) {
    case 'not_found':
      return 'This task is already gone.'
    case 'rate_limited':
      return 'Too many attempts. Please wait a moment and try again.'
    default:
      return 'Something went wrong. Please try again.'
  }
}

/**
 * One task row: display, status controls, edit, delete. Factored out
 * of TaskList — not in CI-8's plan.md file list, but TaskList would
 * otherwise carry every one of these concerns itself. Same kind of
 * necessary, documented deviation as CI-6's RequireAuth/App.test.tsx.
 */
export function TaskItem({ task, onUpdated, onDeleted }: TaskItemProps) {
  const { user } = useAuth()
  const [editing, setEditing] = useState(false)
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const [isDeleting, setIsDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  async function handleConfirmDelete() {
    setDeleteError(null)
    setIsDeleting(true)
    try {
      const response = await apiFetch(`/v1/tasks/${task.id}`, { method: 'DELETE' })
      if (!response.ok) throw response
      setConfirmingDelete(false)
      onDeleted(task.id)
    } catch (err) {
      const classified = err instanceof Response ? await classifyError(err) : null
      setDeleteError(
        classified ? messageForDeleteError(classified) : 'Something went wrong. Please try again.',
      )
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <li className={`${styles.item} ${PRIORITY_STRIPE_CLASS[task.priority] ?? ''}`}>
      {/* Chips left, controls right, both above the title (design
          review). Actions lead the card rather than closing it — they're
          the reason a row is interactive at all, and burying them under
          the description meant the eye travelled past the content to
          reach them on every row. The chips share the line because a
          right-aligned action group over a right-aligned badge group
          stair-stepped down the card's edge with an empty channel
          beside both. */}
      <div className={styles.topRow}>
        {/* The status control sits immediately before the chips it
            changes, so the button reads as acting on the thing next to
            it rather than as one more item in the action group. */}
        <div className={styles.statusGroup}>
          <TaskStatusControls task={task} onSuccess={onUpdated} />
          <span className={`${styles.badge} ${STATUS_BADGE_CLASS[task.status] ?? ''}`}>
            {task.status}
          </span>
          <span className={`${styles.badge} ${PRIORITY_BADGE_CLASS[task.priority] ?? ''}`}>
            {task.priority}
          </span>
        </div>
        <div className={styles.actions}>
          <Button variant="secondary" onClick={() => setEditing(true)}>
            <PencilIcon />
            Edit
          </Button>
          <Button variant="dangerQuiet" onClick={() => setConfirmingDelete(true)}>
            <TrashIcon />
            Delete
          </Button>
        </div>
      </div>
      <div className={styles.written}>
        <h3 className={styles.title}>
          <Mark>{task.title}</Mark>
        </h3>
        {task.description && <p className={styles.description}>{task.description}</p>}
      </div>

      {/* Entire section absent, not just disabled, when attachments are
          off for this account — see plan.md's CI-9 test requirement.
          attachments_enabled comes from GET /auth/me (dual-auth-mode
          CI-8) via useAuth, never an HTTP probe (AM-2). */}
      {user?.attachments_enabled && (
        <div className={styles.attachments}>
          <AttachmentList taskId={task.id} />
        </div>
      )}

      <Modal open={editing} onClose={() => setEditing(false)} title={`Edit "${task.title}"`}>
        <TaskForm
          task={task}
          onCancel={() => setEditing(false)}
          onSuccess={(updated) => {
            setEditing(false)
            onUpdated(updated)
          }}
        />
      </Modal>

      <Modal
        open={confirmingDelete}
        onClose={() => setConfirmingDelete(false)}
        title={`Delete "${task.title}"?`}
      >
        <p className={styles.confirmBody}>This can't be undone.</p>
        {deleteError && (
          <p className={styles.error} role="alert">
            {deleteError}
          </p>
        )}
        <div className={styles.confirmActions}>
          <Button
            variant="secondary"
            onClick={() => setConfirmingDelete(false)}
            disabled={isDeleting}
          >
            Cancel
          </Button>
          <Button variant="danger" loading={isDeleting} onClick={() => void handleConfirmDelete()}>
            Delete
          </Button>
        </div>
      </Modal>
    </li>
  )
}
