/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { ErrorBoundary, SectionErrorFallback } from './ErrorBoundary'

describe('ErrorBoundary.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'ErrorBoundary.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'ErrorBoundary.module.css')
  })
})

/** Throws during render, on purpose, so a test can prove a boundary catches it. */
function Boom(): never {
  throw new Error('boom')
}

describe('ErrorBoundary', () => {
  // React itself also logs a caught render error to console.error (in
  // addition to the componentDidCatch call this suite asserts on) —
  // mocking it keeps that expected noise out of the test's own output
  // without hiding a real assertion failure, which would still throw.
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    consoleErrorSpy.mockRestore()
  })

  it('renders children normally when nothing throws', () => {
    render(
      <ErrorBoundary>
        <p>All good</p>
      </ErrorBoundary>,
    )

    expect(screen.getByText('All good')).toBeInTheDocument()
    expect(consoleErrorSpy).not.toHaveBeenCalled()
  })

  it('catches a render error, shows the default fallback, and leaves a sibling outside the boundary untouched', () => {
    render(
      <div>
        <p>Sibling content survives</p>
        <ErrorBoundary>
          <Boom />
        </ErrorBoundary>
      </div>,
    )

    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong.')
    expect(screen.getByRole('button', { name: 'Reload' })).toBeInTheDocument()
    // The failure was contained: it never propagated up and unmounted
    // content outside this boundary, which is the whole point of a
    // narrower boundary over one that wraps the entire app.
    expect(screen.getByText('Sibling content survives')).toBeInTheDocument()
  })

  it('logs the error and component stack instead of swallowing it', () => {
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    )

    const loggedOwnCall = consoleErrorSpy.mock.calls.find(
      (call: unknown[]) => call[0] === 'Render error caught by ErrorBoundary:',
    )
    expect(loggedOwnCall).toBeDefined()
    expect(loggedOwnCall![1]).toBeInstanceOf(Error)
    expect((loggedOwnCall![1] as Error).message).toBe('boom')
    expect(typeof loggedOwnCall![2]).toBe('string')
  })

  it('renders a custom fallback (e.g. SectionErrorFallback) instead of the default one when given', () => {
    render(
      <ErrorBoundary
        fallback={<SectionErrorFallback message="Something went wrong loading your tasks." />}
      >
        <Boom />
      </ErrorBoundary>,
    )

    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong loading your tasks.')
    expect(screen.queryByText('Something went wrong.')).not.toBeInTheDocument()
  })
})
