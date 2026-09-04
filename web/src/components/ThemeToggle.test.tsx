import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { ThemeToggle } from './ThemeToggle'

function dataTheme(): string | null {
  return document.documentElement.getAttribute('data-theme')
}

function openMenu(user: ReturnType<typeof userEvent.setup>) {
  return user.click(screen.getByRole('button', { name: /^Theme/ }))
}

describe('ThemeToggle', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  afterEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  it('renders an icon-only trigger naming the current choice, defaulting to System', () => {
    render(<ThemeToggle />)

    const trigger = screen.getByRole('button', { name: 'Theme (currently System)' })
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu')
    // Icon-only: the accessible name comes from aria-label, never from
    // visible text competing with the rest of the header.
    expect(trigger).toHaveTextContent('')
  })

  it('the open menu offers all three choices and marks the current one', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await openMenu(user)

    expect(screen.getByRole('menuitemradio', { name: 'System' })).toBeChecked()
    expect(screen.getByRole('menuitemradio', { name: 'Light' })).not.toBeChecked()
    expect(screen.getByRole('menuitemradio', { name: 'Dark' })).not.toBeChecked()
  })

  it('picking "Dark" applies data-theme, persists it, and updates the trigger', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await openMenu(user)
    await user.click(screen.getByRole('menuitemradio', { name: 'Dark' }))

    expect(dataTheme()).toBe('dark')
    expect(localStorage.getItem('theme')).toBe('dark')
    expect(screen.getByRole('button', { name: 'Theme (currently Dark)' })).toBeInTheDocument()
  })

  it('picking "Light" applies data-theme and persists the choice', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await openMenu(user)
    await user.click(screen.getByRole('menuitemradio', { name: 'Light' }))

    expect(dataTheme()).toBe('light')
    expect(localStorage.getItem('theme')).toBe('light')
  })

  it('picking "System" after an explicit choice removes the attribute again', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await openMenu(user)
    await user.click(screen.getByRole('menuitemradio', { name: 'Dark' }))
    expect(dataTheme()).toBe('dark')

    await openMenu(user)
    await user.click(screen.getByRole('menuitemradio', { name: 'System' }))

    expect(dataTheme()).toBeNull()
    expect(localStorage.getItem('theme')).toBeNull()
  })

  it('is operable keyboard-only', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await user.tab()
    const trigger = screen.getByRole('button', { name: /^Theme/ })
    expect(trigger).toHaveFocus()

    // ArrowDown opens the menu with focus on the first item; Enter picks it.
    await user.keyboard('{ArrowDown}')
    expect(screen.getByRole('menuitemradio', { name: 'System' })).toHaveFocus()
    await user.keyboard('{ArrowDown}{Enter}')

    expect(dataTheme()).toBe('light')
  })
})
