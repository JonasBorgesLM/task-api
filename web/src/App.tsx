import { Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from './components/AppShell'
import { ErrorBoundary, SectionErrorFallback } from './components/ErrorBoundary'
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
 * Renders inside AppShell (CI-4 of docs/changes/frontend-redesign) —
 * this component still owns the logout logic (via useAuth), it just
 * hands the actions to the shell instead of rendering its own buttons.
 * "Logged in as {email}" is gone as visible text (see App.test.tsx) —
 * the shell shows the email in its user menu instead, so the same
 * information isn't announced twice on the page.
 */
function AuthenticatedHome() {
  const { user, logout, logoutAll } = useAuth()

  return (
    <AppShell
      userEmail={user?.email}
      onLogout={() => void logout()}
      onLogoutAll={() => void logoutAll()}
    >
      {/* A second, narrower boundary than the app-wide one in main.tsx:
          a malformed task shouldn't also take the header and account
          menu off screen along with the list. */}
      <ErrorBoundary
        fallback={<SectionErrorFallback message="Something went wrong loading your tasks." />}
      >
        <TaskList />
      </ErrorBoundary>
    </AppShell>
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
