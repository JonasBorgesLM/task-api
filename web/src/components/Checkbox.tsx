import type { InputHTMLAttributes } from 'react'
import { useId } from 'react'
import styles from './Checkbox.module.css'

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id' | 'type'> {
  label: string
  error?: string
  id?: string
}

export function Checkbox({ label, error, id, className, ...rest }: CheckboxProps) {
  const generatedId = useId()
  const fieldId = id ?? generatedId
  const errorId = error ? `${fieldId}-error` : undefined

  return (
    <div className={styles.wrapper}>
      <div className={styles.row}>
        <input
          {...rest}
          type="checkbox"
          id={fieldId}
          aria-invalid={error ? true : undefined}
          aria-describedby={errorId}
          className={[styles.input, className].filter(Boolean).join(' ')}
        />
        <label htmlFor={fieldId} className={styles.label}>
          {label}
        </label>
      </div>
      {error && (
        <p id={errorId} className={styles.error} role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
