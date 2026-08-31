import type { SelectHTMLAttributes } from 'react'
import { useId } from 'react'
import styles from './Select.module.css'

export interface SelectProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, 'id'> {
  label: string
  error?: string
  id?: string
}

/**
 * A native <select> — keyboard behavior (typeahead, arrow keys, native
 * mobile picker UI) comes free from the platform. A custom-built
 * listbox would have to reimplement all of that to reach the same
 * baseline, for a component this app doesn't need to visually deviate
 * from native styling to justify.
 */
export function Select({ label, error, id, required, className, children, ...rest }: SelectProps) {
  const generatedId = useId()
  const fieldId = id ?? generatedId
  const errorId = error ? `${fieldId}-error` : undefined

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
      <select
        {...rest}
        id={fieldId}
        required={required}
        aria-invalid={error ? true : undefined}
        aria-describedby={errorId}
        className={[styles.select, error && styles.invalid, className].filter(Boolean).join(' ')}
      >
        {children}
      </select>
      {error && (
        <p id={errorId} className={styles.error} role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
