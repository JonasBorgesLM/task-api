import { useEffect, useId, useRef, useState } from 'react'
import { InfoIcon } from '../../components/icons'
import styles from './TaskStats.module.css'
import { STATUS_LABELS } from './TaskStatusControls'
import type { Task } from './useTasks'

const PRIORITY_LABELS: Record<Task['priority'], string> = {
  low: 'Low',
  medium: 'Medium',
  high: 'High',
}

const STATUS_ORDER: Task['status'][] = ['pending', 'in_progress', 'done', 'cancelled']
const PRIORITY_ORDER: Task['priority'][] = ['high', 'medium', 'low']

export interface TaskStatsProps {
  /** The current page's tasks — what this panel counts, and all it claims to. */
  tasks: Task[]
  /** True when a status/priority filter is narrowing the list. */
  isFiltered: boolean
}

function countBy<K extends string>(tasks: Task[], key: (t: Task) => K): Record<string, number> {
  const out: Record<string, number> = {}
  for (const task of tasks) {
    const k = key(task)
    out[k] = (out[k] ?? 0) + 1
  }
  return out
}

/**
 * The counts panel behind the header's info button — total, then the
 * split by status and by priority.
 *
 * Not built on Menu, deliberately: everything in here is a *fact*, and
 * a role="menu" of things you cannot do would be a lie to anything
 * reading the page aloud. It repeats Menu's dismissal behaviour
 * (Escape, outside click, focus back to the trigger) rather than its
 * markup.
 *
 * Every number counts the page on screen, never a server-side total —
 * GET /v1/tasks does not return one (see useTasks.tsx). So the panel
 * says "on this page", and says so plainly when a filter means these
 * are only the matching ones. A confident-looking total the API never
 * sent would be the one thing worse than no total at all.
 */
export function TaskStats({ tasks, isFiltered }: TaskStatsProps) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const panelId = useId()

  useEffect(() => {
    if (!open) return

    function handlePointerDown(event: MouseEvent) {
      const target = event.target as Node
      if (panelRef.current?.contains(target) || triggerRef.current?.contains(target)) return
      setOpen(false)
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key !== 'Escape') return
      event.preventDefault()
      setOpen(false)
      triggerRef.current?.focus()
    }

    document.addEventListener('mousedown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  const byStatus = countBy(tasks, (t) => t.status)
  const byPriority = countBy(tasks, (t) => t.priority)
  const total = String(tasks.length)

  return (
    <div className={styles.wrapper}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        aria-label={`Task counts (${tasks.length} on this page)`}
        onClick={() => setOpen((value) => !value)}
      >
        <InfoIcon />
      </button>

      {open && (
        <div id={panelId} ref={panelRef} className={styles.panel}>
          <p className={styles.total}>
            <span className={styles.totalValue}>{total}</span>
            <span className={styles.totalLabel}>
              on this page
              {isFiltered ? ', matching the filter' : ''}
            </span>
          </p>

          {/* Each row is a <dt>/<dd> pair inside its own <div>, which is
              the only grouping a <dl> allows. Two <dd>s with no <dt>
              (label and value both as definitions) is what this was
              first, and axe rightly called it: the label is the term. */}
          <div className={styles.group}>
            <p className={styles.groupTitle}>By status</p>
            <dl className={styles.rows}>
              {STATUS_ORDER.map((status) => (
                <div key={status} className={styles.row}>
                  <dt className={styles.rowLabel}>{STATUS_LABELS[status]}</dt>
                  <dd className={styles.rowValue}>{byStatus[status] ?? 0}</dd>
                </div>
              ))}
            </dl>
          </div>

          <div className={styles.group}>
            <p className={styles.groupTitle}>By priority</p>
            <dl className={styles.rows}>
              {PRIORITY_ORDER.map((priority) => (
                <div key={priority} className={styles.row}>
                  <dt className={styles.rowLabel}>{PRIORITY_LABELS[priority]}</dt>
                  <dd className={styles.rowValue}>{byPriority[priority] ?? 0}</dd>
                </div>
              ))}
            </dl>
          </div>
        </div>
      )}
    </div>
  )
}
