import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { Skeleton } from '../../components/Skeleton'
import { useAuth } from './useAuth'

/**
 * Route guard for the authenticated part of the app. Not in CI-6's
 * plan.md file list — see docs/DECISIONS.md's routing decision for why
 * it exists anyway: real URLs only work as "protected" if something
 * redirects an unauthenticated visitor away from them.
 *
 * 'loading' is its own branch, not folded into 'unauthenticated': the
 * cookie is httpOnly, so on every fresh page load this app genuinely
 * does not know yet whether a session exists — redirecting to /login
 * before GET /auth/me answers would flash the login page even for an
 * already-authenticated user reloading /.
 */
export function RequireAuth() {
  const { status } = useAuth()
  const location = useLocation()

  if (status === 'loading') {
    return <Skeleton width="100%" height="2rem" />
  }

  if (status === 'unauthenticated') {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <Outlet />
}
