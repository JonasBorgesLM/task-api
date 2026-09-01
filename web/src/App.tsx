import { Navigate, Route, Routes } from 'react-router-dom'
import { Button } from './components/Button'
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
      <TaskList />
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
