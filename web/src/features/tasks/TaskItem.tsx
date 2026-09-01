import { useState } from 'react'
import { apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import { Button } from '../../components/Button'
import { Modal } from '../../components/Modal'
import styles from './TaskItem.module.css'
import { TaskForm } from './TaskForm'
import { TaskStatusControls } from './TaskStatusControls'
import type { Task } from './useTasks'

export interface TaskItemProps {
  task: Task
  onUpdated: (task: Task) => void
  onDeleted: (id: string) => void
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
    <li className={styles.item}>
      <div className={styles.itemHeader}>
        <h3 className={styles.title}>{task.title}</h3>
        <div className={styles.badges}>
          <span className={styles.badge}>{task.status}</span>
          <span className={styles.badge}>{task.priority}</span>
        </div>
      </div>
      {task.description && <p className={styles.description}>{task.description}</p>}
      <div className={styles.footer}>
        <TaskStatusControls task={task} onSuccess={onUpdated} />
        <div className={styles.actions}>
          <Button variant="secondary" onClick={() => setEditing(true)}>
            Edit
          </Button>
          <Button variant="danger" onClick={() => setConfirmingDelete(true)}>
            Delete
          </Button>
        </div>
      </div>

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
