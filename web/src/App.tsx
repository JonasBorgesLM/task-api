import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import { Skeleton } from './components/Skeleton'
import { RequireAuth } from './features/auth/RequireAuth'

// Chunks past the eagerly-loaded shell (App, RequireAuth, useAuth, the
// design primitives): the unauthenticated area and the authenticated
// one, split at the natural boundary the app already has (issue #203).
// Before this, a first-time visitor to /login downloaded TaskList, its
// attachments UI, and every modal that only makes sense once a session
// exists — none of which /login itself has any use for. SessionsPage
// (issue #224) is its own chunk for the same reason: a visitor who never
// opens "Manage sessions" never downloads it.
const AuthPages = lazy(() =>
  import('./features/auth/AuthPages').then((module) => ({ default: module.AuthPages })),
)
const AuthenticatedHome = lazy(() =>
  import('./AuthenticatedHome').then((module) => ({ default: module.AuthenticatedHome })),
)
const SessionsPage = lazy(() =>
  import('./features/auth/SessionsPage').then((module) => ({ default: module.SessionsPage })),
)

/**
 * Shown while a lazy route's chunk is still downloading — on every
 * visit after the first, that's imperceptibly fast, so this reuses the
 * same bare Skeleton RequireAuth's own 'loading' branch already shows
 * rather than inventing a second loading treatment for it.
 */
function RouteFallback() {
  return <Skeleton width="100%" height="2rem" />
}

function App() {
  return (
    <Routes>
      <Route
        path="/register"
        element={
          <Suspense fallback={<RouteFallback />}>
            <AuthPages page="register" />
          </Suspense>
        }
      />
      <Route
        path="/login"
        element={
          <Suspense fallback={<RouteFallback />}>
            <AuthPages page="login" />
          </Suspense>
        }
      />
      <Route element={<RequireAuth />}>
        <Route
          path="/"
          element={
            <Suspense fallback={<RouteFallback />}>
              <AuthenticatedHome />
            </Suspense>
          }
        />
        <Route
          path="/sessions"
          element={
            <Suspense fallback={<RouteFallback />}>
              <SessionsPage />
            </Suspense>
          }
        />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

export default App
