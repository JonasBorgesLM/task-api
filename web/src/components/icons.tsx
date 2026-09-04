/**
 * App-chrome glyphs — the account menu's trigger and the three theme
 * choices. Same construction as features/tasks/statusIcons.tsx (16px
 * box, 1.5 stroke, `currentColor`, aria-hidden), kept in components/
 * rather than there because none of these belong to the task domain.
 * Still no icon library: five hand-drawn paths is not a dependency's
 * worth of need.
 */
const SHARED_PROPS = {
  viewBox: '0 0 16 16',
  width: 16,
  height: 16,
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.5,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
  'aria-hidden': true,
} as const

/** Head-and-shoulders silhouette — the account menu's trigger. */
export function UserIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <circle cx="8" cy="5.5" r="2.75" />
      <path d="M2.75 13.75c.6-2.75 2.75-4.25 5.25-4.25s4.65 1.5 5.25 4.25" />
    </svg>
  )
}

/** Sun — the "light" theme choice. */
export function SunIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <circle cx="8" cy="8" r="3" />
      <path d="M8 1.25v1.5M8 13.25v1.5M1.25 8h1.5M13.25 8h1.5M3.4 3.4l1.05 1.05M11.55 11.55l1.05 1.05M3.4 12.6l1.05-1.05M11.55 4.45l1.05-1.05" />
    </svg>
  )
}

/** Crescent — the "dark" theme choice. */
export function MoonIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M13.5 9.15A5.75 5.75 0 116.85 2.5a4.5 4.5 0 006.65 6.65z" />
    </svg>
  )
}

/**
 * Half-filled circle — the "system" theme choice, i.e. "whichever the
 * OS is set to". The filled half is the same shape as the outline, so
 * the glyph reads as one circle split down the middle rather than two
 * overlapping shapes.
 */
export function SystemIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <circle cx="8" cy="8" r="5.75" />
      <path d="M8 2.25a5.75 5.75 0 010 11.5z" fill="currentColor" stroke="none" />
    </svg>
  )
}
