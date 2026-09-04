import type { ReactNode } from 'react'
import { MoonIcon, SunIcon, SystemIcon } from './icons'
import type { MenuItem } from './Menu'
import { Menu } from './Menu'
import type { ThemePreference } from './useTheme'
import { useTheme } from './useTheme'

const LABELS: Record<ThemePreference, string> = {
  system: 'System',
  light: 'Light',
  dark: 'Dark',
}

const ICONS: Record<ThemePreference, ReactNode> = {
  system: <SystemIcon />,
  light: <SunIcon />,
  dark: <MoonIcon />,
}

const ORDER: ThemePreference[] = ['system', 'light', 'dark']

/**
 * Fase 14 CI-11, restyled in the design-review pass that followed: an
 * icon-only pull-down (the same Menu primitive CI-12 built), not the
 * labelled <select> this shipped as first. The select was the right
 * call for a form field and the wrong one for app chrome — it carried
 * a visible "Theme" label and a full-width control into a header row
 * whose every other element is a single glyph, which is also how every
 * comparable product (Notion, Linear, Asana) treats this setting.
 *
 * The trigger shows the *current* choice's glyph, and the open menu
 * marks it with a checkmark (MenuItem.selected → role="menuitemradio")
 * — a three-way persistent choice, unlike the status menu's one-shot
 * actions, so it gets the ARIA pattern that actually says so.
 */
export function ThemeToggle() {
  const { preference, setPreference } = useTheme()

  const items: MenuItem[] = ORDER.map((value) => ({
    key: value,
    label: LABELS[value],
    icon: ICONS[value],
    selected: preference === value,
    onSelect: () => setPreference(value),
  }))

  return (
    <Menu
      triggerLabel={`Theme (currently ${LABELS[preference]})`}
      triggerIcon={ICONS[preference]}
      items={items}
      align="end"
    />
  )
}
