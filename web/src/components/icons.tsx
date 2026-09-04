/**
 * The app's glyph set. Same construction throughout (16px box, 1.5
 * stroke, round caps, `currentColor`, aria-hidden), which is what makes
 * a hand-drawn set read as one family rather than as a pile of
 * unrelated marks — every icon here shares a stroke weight and a
 * corner radius with every other, and inherits the colour of whatever
 * it sits in.
 *
 * Kept in components/ because none of these belong to the task domain;
 * the per-status glyphs live in features/tasks/statusIcons.tsx. Still
 * no icon library: a dozen paths is not a dependency's worth of need,
 * and hand-drawing them is what keeps the set consistent with itself.
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

/** Circled "i" — opens the counts panel. */
export function InfoIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <circle cx="8" cy="8" r="6.25" />
      <path d="M8 7.25v4" />
      <path d="M8 4.75h.01" />
    </svg>
  )
}

/** Plus — create. */
export function PlusIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M8 3.25v9.5M3.25 8h9.5" />
    </svg>
  )
}

/** Pencil — edit. */
export function PencilIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M11.4 2.85a1.6 1.6 0 012.25 2.25l-7.4 7.4-3 .75.75-3z" />
    </svg>
  )
}

/** Waste basket — delete. */
export function TrashIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M2.75 4.5h10.5M6.5 4.5V3.25a.75.75 0 01.75-.75h1.5a.75.75 0 01.75.75V4.5" />
      <path d="M4.25 4.5l.6 8.1a.9.9 0 00.9.9h4.5a.9.9 0 00.9-.9l.6-8.1" />
    </svg>
  )
}

/** Paperclip — attachments. */
export function PaperclipIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M12.5 7.6l-5 5a2.9 2.9 0 01-4.1-4.1l5.6-5.6a1.95 1.95 0 012.75 2.75l-5.5 5.5a1 1 0 01-1.4-1.4l5-5" />
    </svg>
  )
}

/** Chevron down — reveal more. */
export function ChevronDownIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M4 6.25L8 10.5l4-4.25" />
    </svg>
  )
}

/** Circular arrow — retry. */
export function RefreshIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M13.25 8a5.25 5.25 0 11-1.55-3.72" />
      <path d="M13.25 2.5v3.25H10" />
    </svg>
  )
}

/** Arrow leaving a doorway — log out of this session. */
export function SignOutIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M6.25 13.25H3.5a.75.75 0 01-.75-.75v-9a.75.75 0 01.75-.75h2.75" />
      <path d="M10 11l3-3-3-3M13 8H6.25" />
    </svg>
  )
}

/** Two screens — the sessions on every other device. */
export function DevicesIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <rect x="1.75" y="3.25" width="8.5" height="6" rx="0.9" />
      <path d="M4.5 11.75h5" />
      <rect x="11" y="7" width="3.25" height="6" rx="0.9" />
    </svg>
  )
}

/** Shield with a check — the security highlight. */
export function ShieldIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M8 1.75l4.75 1.9v4.1c0 3.1-2 5.35-4.75 6.5-2.75-1.15-4.75-3.4-4.75-6.5v-4.1z" />
      <path d="M6 7.9l1.4 1.4 2.6-2.9" />
    </svg>
  )
}

/** Chevron left — the previous page. */
export function ChevronLeftIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M9.75 3.5L5.5 8l4.25 4.5" />
    </svg>
  )
}

/** Chevron right — the next page. */
export function ChevronRightIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M6.25 3.5L10.5 8l-4.25 4.5" />
    </svg>
  )
}
