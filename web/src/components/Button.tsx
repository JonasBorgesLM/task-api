import type { ButtonHTMLAttributes, ReactNode } from 'react'
import styles from './Button.module.css'

/**
 * `danger` is a solid fill, for the one button that actually commits a
 * destructive action — a confirmation dialog's final button. Use
 * `dangerQuiet` for a destructive *entry point* that repeats down a
 * list (a row's Delete): it reads as clearly destructive next to a
 * neutral Edit without turning a list of 16 rows into a wall of solid
 * red, which is exactly how it looked before this variant existed.
 */
export type ButtonVariant = 'primary' | 'secondary' | 'danger' | 'dangerQuiet'

export interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  variant?: ButtonVariant
  /**
   * Disables the button and shows a spinner. Distinct from `disabled`:
   * a caller that only sets `disabled` gets no spinner (e.g. "not
   * allowed yet" states unrelated to an in-flight request).
   */
  loading?: boolean
  children: ReactNode
}

/**
 * Default type="button", not "submit" — a button silently submitting
 * its enclosing form is a common accidental-double-action bug. A form's
 * real submit button passes type="submit" explicitly.
 */
export function Button({
  variant = 'primary',
  loading = false,
  disabled,
  type = 'button',
  className,
  children,
  ...rest
}: ButtonProps) {
  const classes = [styles.button, styles[variant], className].filter(Boolean).join(' ')

  return (
    <button
      {...rest}
      type={type}
      className={classes}
      disabled={disabled ?? loading}
      aria-busy={loading || undefined}
    >
      {loading && <span className={styles.spinner} aria-hidden="true" />}
      {children}
    </button>
  )
}
