import { Navigate, Route, Routes } from 'react-router-dom'
import { Button } from './components/Button'
import { LoginPage } from './features/auth/LoginPage'
import { RegisterPage } from './features/auth/RegisterPage'
import { RequireAuth } from './features/auth/RequireAuth'
import { useAuth } from './features/auth/useAuth'

/**
 * Stands in for the real task list until CI-7 of
 * docs/changes/web-frontend/plan.md replaces it — CI-6's own scope is
 * the auth flow, not task management, but the routing decision it made
 * (see docs/DECISIONS.md) needs a real, working destination behind
 * RequireAuth to actually prove the guard works end to end, not a route
 * that renders nothing.
 */
function AuthenticatedHome() {
  const { user, logout, logoutAll } = useAuth()

  return (
    <div>
      <p>Logged in as {user?.email}</p>
      <Button variant="secondary" onClick={() => void logout()}>
        Log out
      </Button>
      <Button variant="secondary" onClick={() => void logoutAll()}>
        Sign out of all devices
      </Button>
    </div>
  )
}

function App() {
  return (
    <Routes>
      <Route path="/register" element={<RegisterPage />} />
      <Route path="/login" element={<LoginPage />} />
      <Route element={<RequireAuth />}>
        <Route path="/" element={<AuthenticatedHome />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

export default App
