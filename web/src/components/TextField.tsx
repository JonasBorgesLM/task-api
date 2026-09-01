import type { InputHTMLAttributes } from 'react'
import { useId } from 'react'
import styles from './TextField.module.css'

export interface TextFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  label: string
  /** Validation message. Presence alone drives aria-invalid + styling. */
  error?: string
  /** Non-error helper text, e.g. "at least 8 characters". */
  hint?: string
  id?: string
}

/**
 * Always a visible <label>, never a placeholder-only field — a
 * placeholder disappears the moment the user types, which is exactly
 * the moment a person re-reading what they're filling in needs it most.
 */
export function TextField({
  label,
  error,
  hint,
  id,
  required,
  className,
  ...rest
}: TextFieldProps) {
  const generatedId = useId()
  const fieldId = id ?? generatedId
  const hintId = hint ? `${fieldId}-hint` : undefined
  const errorId = error ? `${fieldId}-error` : undefined
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined

  return (
    <div className={styles.field}>
      <label htmlFor={fieldId} className={styles.label}>
        {label}
        {required && (
          <span className={styles.required} aria-hidden="true">
            *
          </span>
        )}
      </label>
      <input
        {...rest}
        id={fieldId}
        required={required}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={[styles.input, error && styles.invalid, className].filter(Boolean).join(' ')}
      />
      {hint && (
        <p id={hintId} className={styles.hint}>
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className={styles.error} role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
