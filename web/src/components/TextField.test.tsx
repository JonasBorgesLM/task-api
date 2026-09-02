/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { TextField } from './TextField'

describe('TextField.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'TextField.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'TextField.module.css')
  })
})

describe('TextField', () => {
  it('associates the visible label with the input via htmlFor/id', () => {
    render(<TextField label="Email" />)
    const input = screen.getByLabelText('Email')
    expect(input.tagName).toBe('INPUT')
  })

  it('is reachable and typeable by keyboard alone', async () => {
    const user = userEvent.setup()
    render(<TextField label="Email" />)

    await user.tab()
    expect(screen.getByLabelText('Email')).toHaveFocus()
    await user.keyboard('alice@example.com')

    expect(screen.getByLabelText('Email')).toHaveValue('alice@example.com')
  })

  it('renders a hint and wires it via aria-describedby', () => {
    render(<TextField label="Password" hint="At least 8 characters" />)
    const input = screen.getByLabelText('Password')
    const hint = screen.getByText('At least 8 characters')

    expect(input.getAttribute('aria-describedby')).toContain(hint.id)
  })

  it('renders an error, sets aria-invalid, and wires it via aria-describedby', () => {
    render(<TextField label="Email" error="Email is required" />)
    const input = screen.getByLabelText('Email')
    const error = screen.getByRole('alert')

    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input.getAttribute('aria-describedby')).toContain(error.id)
    expect(error).toHaveTextContent('Email is required')
  })

  it('has no aria-invalid and no alert when there is no error', () => {
    render(<TextField label="Email" />)
    expect(screen.getByLabelText('Email')).not.toHaveAttribute('aria-invalid')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('marks a required field visually (*) and natively (required attribute)', () => {
    // jsdom's getByLabelText matches raw label textContent, which
    // doesn't model aria-hidden exclusion the way a real screen reader's
    // accessible-name computation does — so this test checks the two
    // concrete, spec-correct signals directly instead of relying on that
    // approximation: the input carries the native `required` attribute
    // (what a screen reader actually announces), and the visual "*" is
    // marked aria-hidden so it isn't redundantly read aloud too.
    render(<TextField label="Email" required />)
    const input = screen.getByRole('textbox')
    expect(input).toBeRequired()
    expect(input).not.toHaveAttribute('aria-label')
    expect(screen.getByText('*')).toHaveAttribute('aria-hidden', 'true')
  })
})
