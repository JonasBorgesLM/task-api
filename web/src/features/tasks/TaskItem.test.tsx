/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateCsrfToken } from '../../api/client'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { TaskItem } from './TaskItem'
import type { Task } from './useTasks'

describe('TaskItem.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'TaskItem.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'TaskItem.module.css')
  })
})

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const TASK: Task = {
  id: 't1',
  title: 'Buy groceries',
  description: 'Milk, eggs, bread',
  status: 'pending',
  priority: 'high',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

// li isn't a valid root for React Testing Library's render() by
// default in terms of semantics — it renders fine, but role/list
// queries expect an ancestor <ul>/<ol>. TaskList (which really owns
// the <ul>) already covers that; here the <li> is wrapped in one just
// so "group"/"listitem" roles resolve the way a browser would.
function renderItem(props: Partial<ComponentProps<typeof TaskItem>> = {}) {
  return render(
    <ul>
      <TaskItem task={TASK} onUpdated={vi.fn()} onDeleted={vi.fn()} {...props} />
    </ul>,
  )
}

describe('TaskItem', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    invalidateCsrfToken()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the task title, description and badges', () => {
    renderItem()

    expect(screen.getByRole('heading', { name: 'Buy groceries' })).toBeInTheDocument()
    expect(screen.getByText('Milk, eggs, bread')).toBeInTheDocument()
    expect(screen.getByText('pending')).toBeInTheDocument()
    expect(screen.getByText('high')).toBeInTheDocument()
  })

  it('a status transition calls onUpdated (TaskStatusControls integration)', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    const updated = { ...TASK, status: 'in_progress' as const }
    fetchMock.mockResolvedValueOnce(jsonResponse(200, updated))
    const onUpdated = vi.fn()
    const user = userEvent.setup()
    renderItem({ onUpdated })

    await user.click(screen.getByRole('button', { name: 'Move to In progress' }))

    await vi.waitFor(() => expect(onUpdated).toHaveBeenCalledWith(updated))
  })

  it('Edit opens a modal with the task form prefilled; saving calls onUpdated and closes it', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    const updated = { ...TASK, title: 'Buy groceries and cook dinner' }
    fetchMock.mockResolvedValueOnce(jsonResponse(200, updated))
    const onUpdated = vi.fn()
    const user = userEvent.setup()
    renderItem({ onUpdated })

    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const dialog = screen.getByRole('dialog', { name: 'Edit "Buy groceries"' })
    expect(dialog).toBeInTheDocument()

    const titleInput = screen.getByLabelText(/^Title/)
    await user.clear(titleInput)
    await user.type(titleInput, 'Buy groceries and cook dinner')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    await vi.waitFor(() => expect(onUpdated).toHaveBeenCalledWith(updated))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('Delete requires explicit confirmation — no request fires until the confirm button is clicked', async () => {
    const fetchMock = vi.mocked(fetch)
    const onDeleted = vi.fn()
    const user = userEvent.setup()
    renderItem({ onDeleted })

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    const dialog = screen.getByRole('dialog', { name: 'Delete "Buy groceries"?' })
    expect(dialog).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(onDeleted).not.toHaveBeenCalled()
  })

  it('Cancel in the confirm dialog closes it without deleting', async () => {
    const fetchMock = vi.mocked(fetch)
    const onDeleted = vi.fn()
    const user = userEvent.setup()
    renderItem({ onDeleted })

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(onDeleted).not.toHaveBeenCalled()
  })

  it('confirming delete DELETEs the task and calls onDeleted', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
    const onDeleted = vi.fn()
    const user = userEvent.setup()
    renderItem({ onDeleted })

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    // Two "Delete" buttons exist now: the row's own, and the confirm
    // dialog's — scope to the dialog to click the right one.
    const dialog = screen.getByRole('dialog', { name: 'Delete "Buy groceries"?' })
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await vi.waitFor(() => expect(onDeleted).toHaveBeenCalledWith('t1'))
    const [url, init] = fetchMock.mock.calls[1]!
    expect(String(url)).toContain('/v1/tasks/t1')
    expect(init!.method).toBe('DELETE')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('a delete failure keeps the confirm dialog open and shows the error, without calling onDeleted', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'tok' }))
    fetchMock.mockResolvedValueOnce(jsonResponse(429, {}))
    const onDeleted = vi.fn()
    const user = userEvent.setup()
    renderItem({ onDeleted })

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    const dialog = screen.getByRole('dialog', { name: 'Delete "Buy groceries"?' })
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/too many attempts/i)
    expect(onDeleted).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
