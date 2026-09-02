/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { Toast } from './Toast'

describe('Toast.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'Toast.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'Toast.module.css')
  })
})

describe('Toast', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('info/success use role="status" (polite)', () => {
    render(<Toast message="Task saved" variant="success" onDismiss={vi.fn()} />)
    expect(screen.getByRole('status')).toHaveTextContent('Task saved')
  })

  it('error uses role="alert" (assertive)', () => {
    render(<Toast message="Something went wrong" variant="error" onDismiss={vi.fn()} />)
    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong')
  })

  it('auto-dismisses an info toast after the default duration', () => {
    const onDismiss = vi.fn()
    render(<Toast message="Task saved" onDismiss={onDismiss} />)

    expect(onDismiss).not.toHaveBeenCalled()
    vi.advanceTimersByTime(5000)
    expect(onDismiss).toHaveBeenCalledOnce()
  })

  it('does NOT auto-dismiss an error toast by default — it waits for the user', () => {
    const onDismiss = vi.fn()
    render(<Toast message="Something went wrong" variant="error" onDismiss={onDismiss} />)

    vi.advanceTimersByTime(60_000)
    expect(onDismiss).not.toHaveBeenCalled()
  })

  it('an explicit duration overrides the variant default', () => {
    const onDismiss = vi.fn()
    render(
      <Toast
        message="Something went wrong"
        variant="error"
        duration={2000}
        onDismiss={onDismiss}
      />,
    )

    vi.advanceTimersByTime(2000)
    expect(onDismiss).toHaveBeenCalledOnce()
  })

  it('the dismiss button calls onDismiss immediately, by keyboard or mouse', async () => {
    vi.useRealTimers()
    const onDismiss = vi.fn()
    const user = userEvent.setup()
    render(<Toast message="Task saved" duration={0} onDismiss={onDismiss} />)

    await user.click(screen.getByRole('button', { name: 'Dismiss' }))

    expect(onDismiss).toHaveBeenCalledOnce()
  })
})
