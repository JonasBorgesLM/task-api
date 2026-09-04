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

  it('"Log out" calls onLogout, "Sign out of all devices" calls onLogoutAll', async () => {
    const onLogout = vi.fn()
    const onLogoutAll = vi.fn()
    const user = userEvent.setup()
    render(
      <AppShell userEmail="alice@example.com" onLogout={onLogout} onLogoutAll={onLogoutAll}>
        <p>content</p>
      </AppShell>,
    )

    await user.click(screen.getByRole('button', { name: 'Log out' }))
    expect(onLogout).toHaveBeenCalledOnce()
    expect(onLogoutAll).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Sign out of all devices' }))
    expect(onLogoutAll).toHaveBeenCalledOnce()
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
