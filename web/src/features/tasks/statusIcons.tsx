import type { Task } from './useTasks'

type Status = Task['status']

interface StatusIconProps {
  status: Status
}

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

/**
 * One small inline glyph per status (Fase 14 CI-12) — shared by
 * TaskStatusControls' menu trigger (shows the task's *current* status)
 * and its menu items (each shows the status it would move *to*), so a
 * user learns "circle = pending" once and it means the same thing in
 * both places. `stroke="currentColor"` rather than a hardcoded color:
 * the glyph inherits whatever text color the button it sits in already
 * has, the same "no dependency without a real need" spirit that keeps
 * this a handful of hand-drawn paths instead of an icon library.
 */
/**
 * The status menu's trigger — one fixed glyph meaning "move this
 * somewhere else", not the current status repeated. Two arrows curving
 * back on each other is the widely-read "change/cycle" mark; showing
 * the current status here instead (as this did first) duplicated the
 * badge sitting next to it and said nothing about what the button
 * does. The per-status glyphs below stay, on the menu's own items,
 * where they identify the destination.
 */
export function StatusChangeIcon() {
  return (
    <svg {...SHARED_PROPS}>
      <path d="M2.75 6.5a5.25 5.25 0 018.9-2.65l1.6 1.4M13.25 9.5a5.25 5.25 0 01-8.9 2.65l-1.6-1.4" />
      <path d="M13.25 2.25v3.25H10M2.75 13.75V10.5H6" />
    </svg>
  )
}

export function StatusIcon({ status }: StatusIconProps) {
  switch (status) {
    case 'pending':
      return (
        <svg {...SHARED_PROPS}>
          <circle cx="8" cy="8" r="5.25" />
        </svg>
      )
    case 'in_progress':
      return (
        <svg {...SHARED_PROPS}>
          <path d="M2.5 8h9M8 4.5 11.5 8 8 11.5" />
        </svg>
      )
    case 'done':
      return (
        <svg {...SHARED_PROPS}>
          <path d="M3 8.5l3 3 7-8" />
        </svg>
      )
    case 'cancelled':
      return (
        <svg {...SHARED_PROPS}>
          <path d="M4 4l8 8M12 4l-8 8" />
        </svg>
      )
  }
}
