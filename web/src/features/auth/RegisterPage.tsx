import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { Link, useNavigate } from 'react-router-dom'
import { z } from 'zod'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import { Button } from '../../components/Button'
import { TextField } from '../../components/TextField'
import { AuthLayout } from './AuthLayout'
import styles from './AuthLayout.module.css'
import { PasswordRequirements } from './PasswordRequirements'
import { useAuth } from './useAuth'

// Mirrors docs/openapi.yaml's RegisterRequest constraints exactly — the
// point of client-side validation is to catch these before a round
// trip, not to invent a stricter policy than the server's. Deliberately
// does NOT also enforce the not-common/not-predictable checks
// PasswordRequirements displays live below the field: those are a
// visual aid (issue #219), never a client-side gate — see that
// component's own doc comment. Only length and the confirmation match
// below block submission.
const registerSchema = z
  .object({
    email: z
      .string()
      .trim()
      .min(1, 'Email is required')
      .max(320)
      .email('Enter a valid email address'),
    password: z
      .string()
      .min(8, 'Must be at least 8 characters')
      .max(72, 'Must be at most 72 characters'),
    confirmPassword: z.string(),
  })
  .refine((values) => values.password === values.confirmPassword, {
    message: 'Passwords do not match',
    path: ['confirmPassword'],
  })

type RegisterFormValues = z.infer<typeof registerSchema>

function messageForError(error: ApiError): string {
  switch (error.kind) {
    case 'invalid_input':
    // The register 409 ("email already registered") is a genuine
    // conflict in the generic sense classifyError uses — the server's
    // own message text is always safe to show verbatim (see
    // errors.ts's ErrorResponse doc comment: "never contains internal
    // details").
    case 'conflict':
      return error.message
    case 'rate_limited':
      return 'Too many attempts. Please wait a moment and try again.'
    default:
      return 'Something went wrong. Please try again.'
  }
}

export function RegisterPage() {
  const { register: registerUser } = useAuth()
  const navigate = useNavigate()
  const [submitError, setSubmitError] = useState<string | null>(null)
  const {
    register,
    handleSubmit,
    control,
    formState: { errors, isSubmitting },
  } = useForm<RegisterFormValues>({ resolver: zodResolver(registerSchema) })
  // useWatch, not the returned watch() function: watch() returns a new
  // function identity every render, which React Compiler flags as an
  // "incompatible library" API it can't safely memoize around.
  const password = useWatch({ control, name: 'password' }) ?? ''

  async function onSubmit(values: RegisterFormValues) {
    setSubmitError(null)
    try {
      await registerUser(values.email, values.password)
      // Register never authenticates the caller (see useAuth.tsx) — the
      // next step is always logging in with the account just created.
      navigate('/login', { state: { justRegistered: true } })
    } catch (err) {
      const classified = err instanceof Response ? await classifyError(err) : null
      setSubmitError(
        classified ? messageForError(classified) : 'Something went wrong. Please try again.',
      )
    }
  }

  return (
    <AuthLayout title="Create an account">
      <form onSubmit={handleSubmit(onSubmit)} noValidate className={styles.form}>
        <TextField
          label="Email"
          type="email"
          autoComplete="email"
          required
          error={errors.email?.message}
          {...register('email')}
        />
        <TextField
          label="Password"
          type="password"
          autoComplete="new-password"
          required
          error={errors.password?.message}
          {...register('password')}
        />
        <PasswordRequirements password={password} />
        <TextField
          label="Confirm password"
          type="password"
          autoComplete="new-password"
          required
          error={errors.confirmPassword?.message}
          {...register('confirmPassword')}
        />
        {submitError && <p role="alert">{submitError}</p>}
        <Button type="submit" loading={isSubmitting}>
          Create account
        </Button>
        <p className={styles.switchLink}>
          Already have an account?{' '}
          <Link to="/login" className={styles.switchLinkAnchor}>
            Log in
          </Link>
        </p>
      </form>
    </AuthLayout>
  )
}
