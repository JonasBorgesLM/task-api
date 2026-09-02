import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { z } from 'zod'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import { Button } from '../../components/Button'
import { PageContainer } from '../../components/PageContainer'
import { TextField } from '../../components/TextField'
import { useAuth } from './useAuth'

// Deliberately no email-format/length validation here (unlike
// RegisterPage) — a malformed login attempt is still just "wrong
// credentials" as far as the user needs to know; the only client-side
// job is catching an empty submission before a round trip.
const loginSchema = z.object({
  email: z.string().trim().min(1, 'Email is required'),
  password: z.string().min(1, 'Password is required'),
})

type LoginFormValues = z.infer<typeof loginSchema>

// This message is the ENTIRE anti-enumeration guarantee on the frontend
// side: classifyError's 'unauthorized' variant carries no message field
// at all (see errors.ts), specifically so there is nothing here that
// could accidentally end up more specific for one case than the other.
// Do not add a branch that distinguishes "unknown email" from "wrong
// password" — the backend deliberately returns the identical response
// for both (see docs/openapi.yaml's /auth/login 401), and a frontend
// that tried to be more helpful here would defeat that.
const INVALID_CREDENTIALS_MESSAGE = 'Invalid email or password.'
const RATE_LIMITED_MESSAGE = 'Too many login attempts. Please wait a moment and try again.'

function messageForError(error: ApiError): string {
  switch (error.kind) {
    case 'unauthorized':
      return INVALID_CREDENTIALS_MESSAGE
    case 'rate_limited':
      return RATE_LIMITED_MESSAGE
    default:
      return 'Something went wrong. Please try again.'
  }
}

interface LocationState {
  from?: { pathname: string }
  justRegistered?: boolean
}

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [submitError, setSubmitError] = useState<string | null>(null)
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginFormValues>({ resolver: zodResolver(loginSchema) })

  const state = location.state as LocationState | null

  async function onSubmit(values: LoginFormValues) {
    setSubmitError(null)
    try {
      await login(values.email, values.password)
      navigate(state?.from?.pathname ?? '/', { replace: true })
    } catch (err) {
      const classified = err instanceof Response ? await classifyError(err) : null
      setSubmitError(
        classified ? messageForError(classified) : 'Something went wrong. Please try again.',
      )
    }
  }

  return (
    <PageContainer>
      <form onSubmit={handleSubmit(onSubmit)} noValidate>
        <h1>Log in</h1>
        {state?.justRegistered && <p role="status">Account created — log in below.</p>}
        <TextField
          label="Email"
          type="email"
          autoComplete="email"
          error={errors.email?.message}
          {...register('email')}
        />
        <TextField
          label="Password"
          type="password"
          autoComplete="current-password"
          error={errors.password?.message}
          {...register('password')}
        />
        {submitError && <p role="alert">{submitError}</p>}
        <Button type="submit" loading={isSubmitting}>
          Log in
        </Button>
        <p>
          Don't have an account? <Link to="/register">Create one</Link>
        </p>
      </form>
    </PageContainer>
  )
}
