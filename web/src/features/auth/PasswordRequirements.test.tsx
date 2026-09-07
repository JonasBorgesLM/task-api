/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { assertOnlyTokens } from '../../test-utils/assertOnlyTokens'
import { PasswordRequirements } from './PasswordRequirements'
import styles from './PasswordRequirements.module.css'

// CSSModuleClasses is an index signature (vite/client), so
// noUncheckedIndexedAccess types every property access as
// `string | undefined` — these two classes are real, defined in
// PasswordRequirements.module.css, so the assertion is safe.
const metClass = styles.met!
const unmetClass = styles.unmet!

describe('PasswordRequirements.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(
      dirname(fileURLToPath(import.meta.url)),
      'PasswordRequirements.module.css',
    )
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'PasswordRequirements.module.css')
  })
})

describe('PasswordRequirements', () => {
  it('shows every requirement as unmet before anything is typed', () => {
    render(<PasswordRequirements password="" />)

    const list = screen.getByRole('list', { name: 'Password requirements' })
    expect(list).toHaveTextContent('At least 8 characters')
    expect(list).toHaveTextContent('Not a commonly used password')
    expect(list).toHaveTextContent('Not a repeated or sequential pattern')
    for (const item of screen.getAllByRole('listitem')) {
      expect(item).toHaveClass(unmetClass)
      expect(item).not.toHaveClass(metClass)
    }
  })

  it('marks length as met once 8 characters are typed, but not a common password', () => {
    render(<PasswordRequirements password="password" />)

    const items = screen.getAllByRole('listitem')
    expect(items[0]).toHaveTextContent('At least 8 characters')
    expect(items[0]).toHaveClass(metClass)

    expect(items[1]).toHaveTextContent('Not a commonly used password')
    expect(items[1]).toHaveClass(unmetClass)
  })

  it('marks every requirement met for a password satisfying all three', () => {
    render(<PasswordRequirements password="correct horse battery staple" />)

    for (const item of screen.getAllByRole('listitem')) {
      expect(item).toHaveClass(metClass)
    }
  })

  it('marks the predictable-pattern requirement unmet for a sequential run', () => {
    render(<PasswordRequirements password="12345678" />)

    const items = screen.getAllByRole('listitem')
    expect(items[2]).toHaveTextContent('Not a repeated or sequential pattern')
    expect(items[2]).toHaveClass(unmetClass)
  })
})
