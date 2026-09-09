/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { ReportPage } from './ReportPage'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function makeTask(overrides: Record<string, unknown> = {}) {
  return {
    id: 't1',
    title: 'Buy groceries',
    description: 'Milk, eggs, bread',
    status: 'pending',
    priority: 'high',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    ...overrides,
  }
}

// A route for "Back to tasks" to land on, and one carrying an initial
// filter — every test that cares about the request/URL renders through
// this, not <ReportPage /> bare, so useNavigate/useSearchParams have
// the Router context they need.
function renderReportPage(initialPath = '/report') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/" element={<p>Task list</p>} />
        <Route path="/report" element={<ReportPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('ReportPage.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'ReportPage.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'ReportPage.module.css')
  })
})

describe('ReportPage', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    window.print = vi.fn()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('fetches the filter carried in the URL, with no page limit', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [makeTask()]))
    renderReportPage('/report?status=pending&status=done&priority=high')

    await screen.findByText('Buy groceries')

    const [url] = fetchMock.mock.calls[0]!
    expect(String(url)).toBe(
      'http://localhost:8080/v1/tasks?status=pending&status=done&priority=high',
    )
  })

  it('renders every matching task as a row, no pagination', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, [makeTask({ id: 't1', title: 'Buy groceries' }), makeTask({ id: 't2', title: 'Walk the dog' })]),
    )
    renderReportPage()

    expect(await screen.findByRole('cell', { name: 'Buy groceries' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Walk the dog' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })

  it('shows the applied filter and a generated timestamp in the header', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    renderReportPage('/report?status=pending&status=done&priority=high')

    await screen.findByText('No tasks match this filter.')

    expect(screen.getByText('Pending, Done')).toBeInTheDocument()
    expect(screen.getByText('High')).toBeInTheDocument()
    expect(screen.getByText('Generated')).toBeInTheDocument()
  })

  it('describes an absent filter as "All statuses"/"All priorities"', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    renderReportPage('/report')

    await screen.findByText('No tasks match this filter.')

    expect(screen.getByText('All statuses')).toBeInTheDocument()
    expect(screen.getByText('All priorities')).toBeInTheDocument()
  })

  it('shows an empty state when nothing matches', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    renderReportPage()

    expect(await screen.findByText('No tasks match this filter.')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('shows an error state and can retry', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(new Response('', { status: 500 }))
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [makeTask()]))
    const user = userEvent.setup()
    renderReportPage()

    expect(await screen.findByRole('alert')).toHaveTextContent(/couldn't load this report/i)

    await user.click(screen.getByRole('button', { name: /retry/i }))

    expect(await screen.findByText('Buy groceries')).toBeInTheDocument()
  })

  it('"Back to tasks" navigates to /', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    const user = userEvent.setup()
    renderReportPage()

    await screen.findByText('No tasks match this filter.')
    await user.click(screen.getByRole('button', { name: 'Back to tasks' }))

    expect(await screen.findByText('Task list')).toBeInTheDocument()
  })

  it('"Print" calls window.print, not a navigation', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []))
    const user = userEvent.setup()
    renderReportPage()

    await screen.findByText('No tasks match this filter.')
    await user.click(screen.getByRole('button', { name: 'Print' }))

    expect(window.print).toHaveBeenCalledOnce()
  })
})
