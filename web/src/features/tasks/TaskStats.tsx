import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { apiFetch } from '../../api/client'
import { Button } from '../../components/Button'
import { InfoIcon, RefreshIcon } from '../../components/icons'
import styles from './TaskStats.module.css'
import { STATUS_LABELS } from './TaskStatusControls'
import type { Task } from './useTasks'
import { splitFilter } from './useTasks'

const PRIORITY_LABELS: Record<Task['priority'], string> = {
  low: 'Low',
  medium: 'Medium',
  high: 'High',
}

const STATUS_ORDER: Task['status'][] = ['pending', 'in_progress', 'done', 'cancelled']
const PRIORITY_ORDER: Task['priority'][] = ['high', 'medium', 'low']

export interface TaskStatsProps {
  /** The count across the whole filtered set, from GET /v1/tasks's X-Total-Count — already fetched by useTasks, so this panel adds no request just to show it. */
  total: number
  /** Same joined "pending,done"/"" filter shape TaskList already computes, passed straight through to GET /v1/tasks/stats. */
  statusFilter: string
  priorityFilter: string
  /** True when a status/priority filter is narrowing the list. */
  isFiltered: boolean
}

type StatsPhase = 'idle' | 'loading' | 'error' | 'loaded'

interface Breakdown {
  byStatus: Record<string, number>
  byPriority: Record<string, number>
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
 * Issue #247/15.G1: every number here used to count only the page on
 * screen, because GET /v1/tasks returned no total at all. With
 * X-Total-Count (issue #237) and GET /v1/tasks/stats (issue #238) both
 * shipped, this panel now says what it means: `total` is free (it rides
 * on the list fetch TaskList already makes), and the by-status/
 * by-priority breakdown is fetched from GET /v1/tasks/stats lazily, the
 * first time the panel actually opens — a caller who never opens this
 * panel never pays for the breakdown request.
 */
export function TaskStats({ total, statusFilter, priorityFilter, isFiltered }: TaskStatsProps) {
  const [open, setOpen] = useState(false)
  const [phase, setPhase] = useState<StatsPhase>('idle')
  const [breakdown, setBreakdown] = useState<Breakdown>({ byStatus: {}, byPriority: {} })
  const triggerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const panelId = useId()

  const loadBreakdown = useCallback(async () => {
    setPhase('loading')
    const params = new URLSearchParams()
    for (const status of splitFilter(statusFilter)) params.append('status', status)
    for (const priority of splitFilter(priorityFilter)) params.append('priority', priority)

    const response = await apiFetch(`/v1/tasks/stats?${params.toString()}`)
    if (!response.ok) {
      setPhase('error')
      return
    }
    const body = (await response.json()) as { by_status: Record<string, number>; by_priority: Record<string, number> }
    setBreakdown({ byStatus: body.by_status, byPriority: body.by_priority })
    setPhase('loaded')
  }, [statusFilter, priorityFilter])

  // Fetches once per (open, filter): opening the panel for the first
  // time loads it, and changing the filter while it's already open
  // re-loads it — but closing and reopening under the *same* filter
  // does not re-fetch, since nothing on screen could have gone stale in
  // that gap without this component's own state changing first. oxlint's
  // set-state-in-effect rule: this is the same "synchronize with an
  // external system" case every other fetch-on-mount in this app
  // already carves out.
  useEffect(() => {
    if (!open) return
    void loadBreakdown()
  }, [open, loadBreakdown])

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

  return (
    <div className={styles.wrapper}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        aria-label={`Task counts (${total}${isFiltered ? ' matching the filter' : ''})`}
        onClick={() => setOpen((value) => !value)}
      >
        <InfoIcon />
      </button>

      {open && (
        <div id={panelId} ref={panelRef} className={styles.panel}>
          <p className={styles.total}>
            <span className={styles.totalValue}>{total}</span>
            <span className={styles.totalLabel}>{isFiltered ? 'matching the filter' : 'total'}</span>
          </p>

          {phase === 'error' && (
            <p className={styles.error} role="alert">
              Couldn't load the breakdown.{' '}
              <Button variant="secondary" onClick={() => void loadBreakdown()}>
                <RefreshIcon />
                Retry
              </Button>
            </p>
          )}

          {/* Each row is a <dt>/<dd> pair inside its own <div>, which is
              the only grouping a <dl> allows. Two <dd>s with no <dt>
              (label and value both as definitions) is what this was
              first, and axe rightly called it: the label is the term. */}
          <div className={styles.group} aria-busy={phase === 'loading'}>
            <p className={styles.groupTitle}>By status</p>
            <dl className={styles.rows}>
              {STATUS_ORDER.map((status) => (
                <div key={status} className={styles.row}>
                  <dt className={styles.rowLabel}>{STATUS_LABELS[status]}</dt>
                  <dd className={styles.rowValue}>
                    {phase === 'loading' || phase === 'idle' ? '—' : (breakdown.byStatus[status] ?? 0)}
                  </dd>
                </div>
              ))}
            </dl>
          </div>

          <div className={styles.group} aria-busy={phase === 'loading'}>
            <p className={styles.groupTitle}>By priority</p>
            <dl className={styles.rows}>
              {PRIORITY_ORDER.map((priority) => (
                <div key={priority} className={styles.row}>
                  <dt className={styles.rowLabel}>{PRIORITY_LABELS[priority]}</dt>
                  <dd className={styles.rowValue}>
                    {phase === 'loading' || phase === 'idle' ? '—' : (breakdown.byPriority[priority] ?? 0)}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        </div>
      )}
    </div>
  )
}
