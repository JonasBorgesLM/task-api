/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import type { ComponentProps } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { Select } from './Select'

describe('Select.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'Select.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'Select.module.css')
  })
})

function renderPrioritySelect(extraProps: Partial<ComponentProps<typeof Select>> = {}) {
  return render(
    <Select label="Priority" {...extraProps}>
      <option value="low">Low</option>
      <option value="medium">Medium</option>
      <option value="high">High</option>
    </Select>,
  )
}

describe('Select', () => {
  it('associates the visible label with a native <select>', () => {
    renderPrioritySelect()
    const select = screen.getByLabelText('Priority')
    expect(select.tagName).toBe('SELECT')
  })

  it('is operable by keyboard alone — tab to focus, arrow keys change value', async () => {
    const user = userEvent.setup()
    renderPrioritySelect()

    await user.tab()
    const select = screen.getByLabelText('Priority') as HTMLSelectElement
    expect(select).toHaveFocus()

    await user.selectOptions(select, 'high')
    expect(select).toHaveValue('high')
  })

  it('renders an error, sets aria-invalid, and wires it via aria-describedby', () => {
    renderPrioritySelect({ error: 'Priority is required' })
    const select = screen.getByLabelText('Priority')
    const error = screen.getByRole('alert')

    expect(select).toHaveAttribute('aria-invalid', 'true')
    expect(select.getAttribute('aria-describedby')).toBe(error.id)
  })

  it('has no aria-invalid and no alert when there is no error', () => {
    renderPrioritySelect()
    expect(screen.getByLabelText('Priority')).not.toHaveAttribute('aria-invalid')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
