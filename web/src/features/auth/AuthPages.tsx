import { LoginPage } from './LoginPage'
import { RegisterPage } from './RegisterPage'

export interface AuthPagesProps {
  page: 'login' | 'register'
}

/**
 * The whole unauthenticated area — LoginPage, RegisterPage, and the
 * AuthLayout shell both sit inside — bundled behind one dynamic import
 * (see App.tsx's lazy() call). Visiting either page is what used to
 * also download the authenticated area's own code (issue #203):
 * TaskList, its attachments, and every modal that only makes sense once
 * a session exists. One module rather than a separate lazy() per page
 * is deliberate — the point is one "auth" chunk, not two smaller ones
 * that still both ship before a session exists.
 */
export function AuthPages({ page }: AuthPagesProps) {
  return page === 'login' ? <LoginPage /> : <RegisterPage />
}
