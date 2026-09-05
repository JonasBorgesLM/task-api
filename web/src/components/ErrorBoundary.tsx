import type { ErrorInfo, ReactNode } from 'react'
import { Component } from 'react'
import { Button } from './Button'
import styles from './ErrorBoundary.module.css'

export interface ErrorBoundaryProps {
  children: ReactNode
  /**
   * Rendered in place of children once an error is caught. Defaults to
   * a full-page-sized message — pass a smaller one for a boundary
   * placed around a section rather than the whole app, so the fallback
   * doesn't look like the app just crashed when only one part did.
   */
  fallback?: ReactNode
}

interface ErrorBoundaryState {
  hasError: boolean
}

/**
 * Catches a render error anywhere in its subtree instead of letting it
 * unmount the whole app to a blank screen (issue #202) — the one
 * failure mode nothing else here already handles: useTasks and
 * AttachmentList have error states for network failure, useAuth
 * distinguishes 401 from 503, but none of that runs for an exception
 * thrown during render itself.
 *
 * A class component because getDerivedStateFromError/componentDidCatch
 * have no hook equivalent — React does not offer a functional error
 * boundary.
 *
 * Recovery is a full reload, not a "try again" that just clears
 * hasError: the render that failed would run again with the same
 * props and the same bad data, and fail again. A reload is the one
 * recovery that's actually different from what just failed.
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { hasError: false }

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { hasError: true }
  }

  // Logged, never swallowed — a caught render error that vanishes
  // silently just trades a blank screen for an invisible failure.
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Render error caught by ErrorBoundary:', error, info.componentStack)
  }

  render() {
    if (this.state.hasError) {
      return this.props.fallback ?? <DefaultFallback />
    }
    return this.props.children
  }
}

function DefaultFallback() {
  return (
    <div className={styles.fallback} role="alert">
      <p className={styles.heading}>Something went wrong.</p>
      <p className={styles.message}>
        This page hit an unexpected error. Reloading usually fixes it.
      </p>
      <Button onClick={() => window.location.reload()}>Reload</Button>
    </div>
  )
}

/**
 * A smaller fallback for a boundary placed around one section (e.g.
 * `<ErrorBoundary fallback={<SectionErrorFallback message="..." />}>`)
 * rather than the whole app — so a malformed item in that one section
 * doesn't also take the surrounding chrome off screen.
 */
export function SectionErrorFallback({ message }: { message: string }) {
  return (
    <div className={`${styles.fallback} ${styles.compact}`} role="alert">
      <p className={styles.message}>{message}</p>
      <Button variant="secondary" onClick={() => window.location.reload()}>
        Reload
      </Button>
    </div>
  )
}
