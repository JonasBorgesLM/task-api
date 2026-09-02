import { Navigate, Route, Routes } from 'react-router-dom'
import { Button } from './components/Button'
import { PageContainer } from './components/PageContainer'
import { LoginPage } from './features/auth/LoginPage'
import { RegisterPage } from './features/auth/RegisterPage'
import { RequireAuth } from './features/auth/RequireAuth'
import { useAuth } from './features/auth/useAuth'
import { TaskList } from './features/tasks/TaskList'

/**
 * CI-7 of docs/changes/web-frontend/plan.md: the real task list,
 * replacing CI-6's placeholder. logout/logout-all stay here rather than
 * moving into TaskList — they're session actions, not task-list
 * concerns, and TaskList has no reason to know useAuth exists.
 *
 * Wrapped in PageContainer (CI-2 of docs/changes/frontend-redesign) for
 * the same deliberate content width login/register now use — CI-4 of
 * that same phase replaces the bare paragraph+buttons below with a real
 * app-shell; this only fixes the width in the meantime.
 */
function AuthenticatedHome() {
  const { user, logout, logoutAll } = useAuth()

  return (
    <PageContainer>
      <p>Logged in as {user?.email}</p>
      <Button variant="secondary" onClick={() => void logout()}>
        Log out
      </Button>
      <Button variant="secondary" onClick={() => void logoutAll()}>
        Sign out of all devices
      </Button>
      <TaskList />
    </PageContainer>
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
