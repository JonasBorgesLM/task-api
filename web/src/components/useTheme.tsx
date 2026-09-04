import { useCallback, useEffect, useState } from 'react'

export type ThemePreference = 'system' | 'light' | 'dark'

// UI preference, not a session credential — docs/DECISIONS.md § "Cookie
// httpOnly, nunca localStorage" is about never storing the session
// token or CSRF token here (the whole reason the backend issues an
// httpOnly cookie in the first place); a light/dark choice carries no
// such risk. index.html's inline script reads this exact same key
// before React mounts, to avoid a flash of the wrong theme on reload —
// see its own comment.
const STORAGE_KEY = 'theme'

function isExplicitPreference(value: string | null): value is 'light' | 'dark' {
  return value === 'light' || value === 'dark'
}

function readStoredPreference(): ThemePreference {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    return isExplicitPreference(stored) ? stored : 'system'
  } catch {
    // Private browsing / localStorage disabled — same fallback as
    // "no choice made yet": system preference via tokens.css's own
    // @media rule, which needs no JS at all to work correctly.
    return 'system'
  }
}

// tokens.css's two activation paths key off this exact attribute:
// @media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) }
// picks up "system" and "dark" alike (removing the attribute, or setting
// it to "dark", both leave that :not() true); :root[data-theme="dark"]
// is what makes an explicit dark choice win even when the OS prefers
// light. "system" therefore means "no attribute at all", not a third
// literal value — tokens.css has no [data-theme="system"] selector to
// match one.
function applyPreference(preference: ThemePreference) {
  const root = document.documentElement
  if (preference === 'system') {
    root.removeAttribute('data-theme')
  } else {
    root.setAttribute('data-theme', preference)
  }
}

/**
 * Fase 14 CI-11. System preference is the default until an explicit
 * choice exists — the same rule tokens.css's own comment describes.
 * Reading localStorage happens once, in useState's lazy initializer,
 * so the very first render already reflects any stored choice (index.html's
 * inline script already applied it to the DOM before React mounted;
 * this just brings ThemeToggle's own UI state into agreement with it).
 */
export function useTheme() {
  const [preference, setPreferenceState] = useState<ThemePreference>(readStoredPreference)

  useEffect(() => {
    applyPreference(preference)
  }, [preference])

  const setPreference = useCallback((next: ThemePreference) => {
    setPreferenceState(next)
    try {
      if (next === 'system') {
        localStorage.removeItem(STORAGE_KEY)
      } else {
        localStorage.setItem(STORAGE_KEY, next)
      }
    } catch {
      // Preference still applies for this page view via the effect
      // above — it just won't survive a reload. Same degraded-but-
      // functional fallback as readStoredPreference's catch.
    }
  }, [])

  return { preference, setPreference }
}
