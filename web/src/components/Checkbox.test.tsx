/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { Checkbox } from './Checkbox'

describe('Checkbox.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'Checkbox.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'Checkbox.module.css')
  })
})

describe('Checkbox', () => {
  it('associates the visible label with a native checkbox input', () => {
    render(<Checkbox label="Remember me" />)
    const checkbox = screen.getByRole('checkbox', { name: 'Remember me' })
    expect(checkbox).toHaveAttribute('type', 'checkbox')
  })

  it('is toggleable by keyboard alone (tab, then Space)', async () => {
    const user = userEvent.setup()
    render(<Checkbox label="Remember me" />)

    await user.tab()
    const checkbox = screen.getByRole('checkbox', { name: 'Remember me' })
    expect(checkbox).toHaveFocus()
    expect(checkbox).not.toBeChecked()

    await user.keyboard(' ')
    expect(checkbox).toBeChecked()
  })

  it('is also toggleable by clicking the label text, not just the box', async () => {
    const user = userEvent.setup()
    render(<Checkbox label="Remember me" />)

    await user.click(screen.getByText('Remember me'))

    expect(screen.getByRole('checkbox', { name: 'Remember me' })).toBeChecked()
  })

  it('renders an error, sets aria-invalid, and wires it via aria-describedby', () => {
    render(<Checkbox label="Accept terms" error="You must accept the terms" />)
    const checkbox = screen.getByRole('checkbox', { name: 'Accept terms' })
    const error = screen.getByRole('alert')

    expect(checkbox).toHaveAttribute('aria-invalid', 'true')
    expect(checkbox.getAttribute('aria-describedby')).toBe(error.id)
  })
})
