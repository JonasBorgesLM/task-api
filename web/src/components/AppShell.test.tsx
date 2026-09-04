/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { AppShell } from './AppShell'

describe('AppShell.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'AppShell.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'AppShell.module.css')
  })
})

describe('AppShell', () => {
  it('renders the app name and the current user email', () => {
    render(
      <AppShell userEmail="alice@example.com" onLogout={vi.fn()} onLogoutAll={vi.fn()}>
        <p>content</p>
      </AppShell>,
    )

    expect(screen.getByText('Task API')).toBeInTheDocument()
    expect(screen.getByText('alice@example.com')).toBeInTheDocument()
  })

  it('renders header and navigation landmarks', () => {
    render(
      <AppShell userEmail="alice@example.com" onLogout={vi.fn()} onLogoutAll={vi.fn()}>
        <p>content</p>
      </AppShell>,
    )

    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Main' })).toBeInTheDocument()
  })

  it('"Log out" is behind the account menu and calls onLogout directly', async () => {
    const onLogout = vi.fn()
    const onLogoutAll = vi.fn()
    const user = userEvent.setup()
    render(
      <AppShell userEmail="alice@example.com" onLogout={onLogout} onLogoutAll={onLogoutAll}>
        <p>content</p>
      </AppShell>,
    )

    // Not in the header until the menu is opened — that's the point of
    // consolidating them behind one trigger.
    expect(screen.queryByRole('menuitem', { name: 'Log out' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Account: alice@example.com' }))
    await user.click(screen.getByRole('menuitem', { name: 'Log out' }))

    expect(onLogout).toHaveBeenCalledOnce()
    expect(onLogoutAll).not.toHaveBeenCalled()
  })

  it('"Sign out of all devices" asks for confirmation first — no call until confirmed', async () => {
    const onLogoutAll = vi.fn()
    const user = userEvent.setup()
    render(
      <AppShell userEmail="alice@example.com" onLogout={vi.fn()} onLogoutAll={onLogoutAll}>
        <p>content</p>
      </AppShell>,
    )

    await user.click(screen.getByRole('button', { name: 'Account: alice@example.com' }))
    await user.click(screen.getByRole('menuitem', { name: 'Sign out of all devices' }))

    const dialog = screen.getByRole('dialog', { name: 'Sign out of all devices?' })
    expect(dialog).toBeInTheDocument()
    // The dialog has to actually explain the consequence, not just ask.
    expect(dialog).toHaveTextContent(/every other device or browser/i)
    expect(onLogoutAll).not.toHaveBeenCalled()
  })

  it('cancelling the sign-out-everywhere confirmation closes it without calling onLogoutAll', async () => {
    const onLogoutAll = vi.fn()
    const user = userEvent.setup()
    render(
      <AppShell userEmail="alice@example.com" onLogout={vi.fn()} onLogoutAll={onLogoutAll}>
        <p>content</p>
      </AppShell>,
    )

    await user.click(screen.getByRole('button', { name: 'Account: alice@example.com' }))
    await user.click(screen.getByRole('menuitem', { name: 'Sign out of all devices' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(onLogoutAll).not.toHaveBeenCalled()
  })

  it('confirming the dialog calls onLogoutAll and closes it', async () => {
    const onLogoutAll = vi.fn()
    const user = userEvent.setup()
    render(
      <AppShell userEmail="alice@example.com" onLogout={vi.fn()} onLogoutAll={onLogoutAll}>
        <p>content</p>
      </AppShell>,
    )

    await user.click(screen.getByRole('button', { name: 'Account: alice@example.com' }))
    await user.click(screen.getByRole('menuitem', { name: 'Sign out of all devices' }))
    await user.click(screen.getByRole('button', { name: 'Sign out everywhere' }))

    expect(onLogoutAll).toHaveBeenCalledOnce()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('no longer renders a static "Tasks" section label beside the product name', () => {
    render(
      <AppShell userEmail="alice@example.com" onLogout={vi.fn()} onLogoutAll={vi.fn()}>
        <p>content</p>
      </AppShell>,
    )

    expect(screen.getByRole('navigation', { name: 'Main' })).toHaveTextContent('Task API')
    expect(screen.getByRole('navigation', { name: 'Main' })).not.toHaveTextContent('Tasks')
  })

  it('renders its children inside the main landmark', () => {
    render(
      <AppShell userEmail="alice@example.com" onLogout={vi.fn()} onLogoutAll={vi.fn()}>
        <p>task list goes here</p>
      </AppShell>,
    )

    expect(screen.getByRole('main')).toHaveTextContent('task list goes here')
  })
})
