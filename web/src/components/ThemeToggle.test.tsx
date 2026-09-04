import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { ThemeToggle } from './ThemeToggle'

function dataTheme(): string | null {
  return document.documentElement.getAttribute('data-theme')
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

  it('renders a labeled select, defaulting to "System"', () => {
    render(<ThemeToggle />)

    const select = screen.getByRole('combobox', { name: 'Theme' })
    expect(select).toHaveValue('system')
  })

  it('picking "Dark" applies data-theme and persists the choice', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await user.selectOptions(screen.getByRole('combobox', { name: 'Theme' }), 'Dark')

    expect(dataTheme()).toBe('dark')
    expect(localStorage.getItem('theme')).toBe('dark')
  })

  it('picking "Light" applies data-theme and persists the choice', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await user.selectOptions(screen.getByRole('combobox', { name: 'Theme' }), 'Light')

    expect(dataTheme()).toBe('light')
    expect(localStorage.getItem('theme')).toBe('light')
  })

  it('picking "System" after an explicit choice removes the attribute again', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)
    const select = screen.getByRole('combobox', { name: 'Theme' })

    await user.selectOptions(select, 'Dark')
    expect(dataTheme()).toBe('dark')

    await user.selectOptions(select, 'System')
    expect(dataTheme()).toBeNull()
    expect(localStorage.getItem('theme')).toBeNull()
  })

  it('is operable keyboard-only', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await user.tab()
    expect(screen.getByRole('combobox', { name: 'Theme' })).toHaveFocus()
  })
})
