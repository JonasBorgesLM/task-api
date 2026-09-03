/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { AuthLayout } from './AuthLayout'

describe('AuthLayout.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'AuthLayout.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'AuthLayout.module.css')
  })
})

describe('AuthLayout', () => {
  it('renders the title, the app name, and its children inside the card', () => {
    render(
      <AuthLayout title="Log in">
        <button type="button">Submit</button>
      </AuthLayout>,
    )

    expect(screen.getByRole('heading', { name: 'Log in' })).toBeInTheDocument()
    expect(screen.getByText('Task API')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Submit' })).toBeInTheDocument()
  })

  it('renders a labeled marketing panel with real product information, not decorative filler', () => {
    render(
      <AuthLayout title="Log in">
        <button type="button">Submit</button>
      </AuthLayout>,
    )

    const panel = screen.getByRole('complementary', { name: 'About Task API' })
    expect(panel).toHaveTextContent('Full task lifecycle')
    expect(panel).toHaveTextContent('File attachments')
    expect(panel).toHaveTextContent('Secure by design')
  })

  it('keeps the form reachable before the marketing panel in tab order', async () => {
    const user = userEvent.setup()
    render(
      <AuthLayout title="Log in">
        <button type="button">First form control</button>
      </AuthLayout>,
    )

    await user.tab()
    expect(screen.getByRole('button', { name: 'First form control' })).toHaveFocus()
  })
})
