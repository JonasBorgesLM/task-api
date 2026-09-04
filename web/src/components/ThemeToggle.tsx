import type { ChangeEvent } from 'react'
import { Select } from './Select'
import styles from './ThemeToggle.module.css'
import type { ThemePreference } from './useTheme'
import { useTheme } from './useTheme'

function isThemePreference(value: string): value is ThemePreference {
  return value === 'system' || value === 'light' || value === 'dark'
}

/**
 * Fase 14 CI-11 — lives in AppShell's user menu. A native <select>
 * (via the already-restyled Select from CI-3), not a custom
 * icon-button group: this is a three-way choice (system/light/dark),
 * exactly the kind of control HTML already has a real, fully
 * accessible widget for — same "semantic HTML first" reasoning Select
 * and Checkbox were already built on. CI-12's icon-menu redesign is
 * about TaskStatusControls specifically (a different interaction,
 * "pick one of several actions to perform now"), not a template to
 * force onto every control in the app.
 */
export function ThemeToggle() {
  const { preference, setPreference } = useTheme()

  function handleChange(event: ChangeEvent<HTMLSelectElement>) {
    const { value } = event.target
    if (isThemePreference(value)) setPreference(value)
  }

  return (
    <div className={styles.container}>
      <Select label="Theme" value={preference} onChange={handleChange}>
        <option value="system">System</option>
        <option value="light">Light</option>
        <option value="dark">Dark</option>
      </Select>
    </div>
  )
}
