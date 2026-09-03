import { useState } from 'react'
import { apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import { Button } from '../../components/Button'
import styles from './TaskStatusControls.module.css'
import type { Task } from './useTasks'

type Status = Task['status']

const ALL_STATUSES: Status[] = ['pending', 'in_progress', 'done', 'cancelled']

// Exported for reuse by TaskList's status filter (CI-15) — a single
// source of human-readable status text, so the filter dropdown and the
// transition menu never drift apart on wording.
export const STATUS_LABELS: Record<Status, string> = {
  pending: 'Pending',
  in_progress: 'In progress',
  done: 'Done',
  cancelled: 'Cancelled',
}

/**
 * Mirrors internal/task/service.go's legalTransitions table exactly —
 * see CLAUDE.md's "Task status: transition rules live in Service, not
 * the database". This decides ONLY which buttons render enabled; the
 * server stays the authority regardless, which is why handleTransition
 * below still handles a 409 on click rather than trusting this table
 * blindly — the mirror can go stale (another tab, another device) in
 * ways this component has no way to detect ahead of time.
 */
const LEGAL_TRANSITIONS: Record<Status, Status[]> = {
  pending: ['in_progress', 'done', 'cancelled'],
  in_progress: ['pending', 'done', 'cancelled'],
  done: ['pending', 'in_progress'],
  cancelled: ['pending'],
}

function messageForError(error: ApiError): string {
  if (error.kind === 'conflict') {
    if (error.reason === 'invalid_transition') {
      return "This task's status changed elsewhere, and this move is no longer allowed. Refresh to see its current status."
    }
    // 'concurrency' and 'unknown' both get the same generic message —
    // see TaskForm.tsx's identical reasoning: there's no version to
    // diff against, so a specific message would be guessing.
    return 'Someone else changed this task at the same time. Please try again.'
  }
  if (error.kind === 'rate_limited') {
    return 'Too many attempts. Please wait a moment and try again.'
  }
  return 'Something went wrong. Please try again.'
}

export interface TaskStatusControlsProps {
  task: Task
  onSuccess: (task: Task) => void
}

export function TaskStatusControls({ task, onSuccess }: TaskStatusControlsProps) {
  const [pendingTarget, setPendingTarget] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function handleTransition(target: Status) {
    setError(null)
    setPendingTarget(target)
    try {
      const response = await apiFetch(`/v1/tasks/${task.id}/status`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status: target }),
      })
      if (!response.ok) throw response
      onSuccess((await response.json()) as Task)
    } catch (err) {
      const classified = err instanceof Response ? await classifyError(err) : null
      setError(classified ? messageForError(classified) : 'Something went wrong. Please try again.')
    } finally {
      setPendingTarget(null)
    }
  }

  const otherStatuses = ALL_STATUSES.filter((status) => status !== task.status)

  return (
    <div>
      <div className={styles.controls} role="group" aria-label={`Change status of "${task.title}"`}>
        {otherStatuses.map((target) => (
          <Button
            key={target}
            variant="secondary"
            disabled={!LEGAL_TRANSITIONS[task.status].includes(target)}
            loading={pendingTarget === target}
            onClick={() => void handleTransition(target)}
          >
            Move to {STATUS_LABELS[target]}
          </Button>
        ))}
      </div>
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
