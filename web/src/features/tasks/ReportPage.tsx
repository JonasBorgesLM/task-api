import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import { Button } from '../../components/Button'
import { PageContainer } from '../../components/PageContainer'
import { PrinterIcon, RefreshIcon } from '../../components/icons'
import { Skeleton } from '../../components/Skeleton'
import styles from './ReportPage.module.css'
import { PRIORITY_LABELS } from './TaskList'
import { STATUS_LABELS } from './TaskStatusControls'
import type { Task } from './useTasks'

type ReportStatus = 'loading' | 'empty' | 'error' | 'success'

const SKELETON_ROWS = 5

function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

/** ["pending", "done"] -> "Pending, Done"; [] -> "All statuses"/"All priorities". */
function filterSummary(values: string[], labels: Record<string, string>, allLabel: string): string {
  if (values.length === 0) return allLabel
  return values.map((value) => labels[value] ?? value).join(', ')
}

/**
 * `/report` — the printable read-only view issue #246 (15.F8) chose
 * over `@media print` on the list itself: `GET /v1/tasks` returns at
 * most 10 rows a page, so printing the screen directly would silently
 * show only the first page with nothing telling the reader more exist.
 * This route fetches the *entire* filtered set instead (the same
 * "absent limit means no limit" behaviour `GET /v1/tasks` already
 * promises — no new backend capability needed) and renders it for
 * reading, not for operating on: no menus, no pagination, no per-row
 * actions. See docs/DECISIONS.md's task-export section for the sibling
 * decision (CSV, issue #239) this one shares its reasoning with.
 *
 * The filter is carried in the URL (`?status=&priority=`), not passed
 * in as props from TaskList — this address is meant to be printed,
 * bookmarked, or reloaded on its own, and doing that has to reproduce
 * the same report rather than showing whatever TaskList last held in
 * memory.
 *
 * Its own route/chunk (see App.tsx), and deliberately outside AppShell:
 * the account menu, theme toggle and product mark are exactly the
 * on-screen chrome issue #245's print stylesheet has to hide, so this
 * page never renders them in the first place rather than fighting them
 * with `@media print` overrides.
 */
export function ReportPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const statuses = searchParams.getAll('status')
  const priorities = searchParams.getAll('priority')
  const searchKey = searchParams.toString()

  const [status, setStatus] = useState<ReportStatus>('loading')
  const [tasks, setTasks] = useState<Task[]>([])
  const [error, setError] = useState<ApiError | null>(null)
  // Frozen once, at mount: re-rendering (e.g. the Retry button) must not
  // change what "Generated on" reports, or the timestamp would describe
  // the render instead of the data underneath it.
  const [generatedAt] = useState(() => new Date())

  // Depends on searchKey (the whole of searchParams, as one string) so
  // reloading fires exactly once per distinct filter — the same reason
  // useTasks.tsx compares a joined filterKey rather than the individual
  // status/priority values.
  const load = useCallback(async () => {
    setStatus('loading')
    const response = await apiFetch(`/v1/tasks?${searchKey}`)
    if (!response.ok) {
      setError(await classifyError(response))
      setStatus('error')
      return
    }
    const body = (await response.json()) as Task[]
    setTasks(body)
    setError(null)
    setStatus(body.length === 0 ? 'empty' : 'success')
  }, [searchKey])

  // oxlint's set-state-in-effect rule: fetch-on-mount (and re-fetch when
  // the filter in the URL changes) is the canonical "synchronize with
  // an external system" case its own guidance carves out — same
  // accepted pattern as useTasks.tsx and SessionsPage.tsx.
  useEffect(() => {
    void load()
  }, [load])

  return (
    <PageContainer>
      {/* Hidden by ReportPage.module.css's own @media print rule (issue
          #245) — nothing here is part of the report itself. */}
      <div className={styles.topBar}>
        <Button variant="secondary" onClick={() => navigate('/')}>
          Back to tasks
        </Button>
        <Button onClick={() => window.print()}>
          <PrinterIcon />
          Print
        </Button>
      </div>

      <header className={styles.reportHeader}>
        <h1 className={styles.title}>Task report</h1>
        <dl className={styles.meta}>
          <div>
            <dt>Status</dt>
            <dd>{filterSummary(statuses, STATUS_LABELS, 'All statuses')}</dd>
          </div>
          <div>
            <dt>Priority</dt>
            <dd>{filterSummary(priorities, PRIORITY_LABELS, 'All priorities')}</dd>
          </div>
          <div>
            <dt>Generated</dt>
            <dd>{formatDateTime(generatedAt.toISOString())}</dd>
          </div>
        </dl>
      </header>

      {status === 'loading' && (
        <div aria-busy="true" aria-label="Loading report">
          {Array.from({ length: SKELETON_ROWS }, (_, i) => (
            <div key={i} className={styles.skeletonRow}>
              <Skeleton width="100%" height="1.5rem" />
            </div>
          ))}
        </div>
      )}

      {status === 'error' && (
        <p className={styles.error} role="alert">
          {error?.kind === 'unavailable'
            ? "Couldn't load this report — the service is temporarily unavailable. "
            : "Couldn't load this report. "}
          <Button variant="secondary" onClick={() => void load()}>
            <RefreshIcon />
            Retry
          </Button>
        </p>
      )}

      {status === 'empty' && <p className={styles.empty}>No tasks match this filter.</p>}

      {status === 'success' && (
        <table className={styles.table}>
          <thead>
            <tr>
              <th>Title</th>
              <th>Description</th>
              <th>Status</th>
              <th>Priority</th>
              <th>Created</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {tasks.map((task) => (
              <tr key={task.id}>
                <td>{task.title}</td>
                <td>{task.description}</td>
                <td>
                  <span className={styles.badge}>{STATUS_LABELS[task.status]}</span>
                </td>
                <td>
                  <span className={styles.badge}>{PRIORITY_LABELS[task.priority]}</span>
                </td>
                <td>{formatDateTime(task.created_at)}</td>
                <td>{formatDateTime(task.updated_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </PageContainer>
  )
}
