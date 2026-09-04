import type { KeyboardEvent as ReactKeyboardEvent, ReactNode } from 'react'
import { useEffect, useId, useRef, useState } from 'react'
import { ChevronDownIcon } from './icons'
import styles from './Menu.module.css'

export interface MenuItem {
  key: string
  label: string
  icon?: ReactNode
  onSelect: () => void
  /**
   * Present only for a menu whose items are a mutually-exclusive
   * *choice* rather than one-shot actions (the theme menu; not the
   * status menu, whose items each perform a transition and then stop
   * being relevant). Setting it switches the item to
   * role="menuitemradio" with aria-checked — the ARIA pattern for
   * exactly that distinction — and draws a checkmark on the current
   * one, the way a native OS pull-down marks its active entry.
   */
  selected?: boolean
}

export interface MenuProps {
  /** Accessible name for the icon-only trigger button. */
  triggerLabel: string
  /** Small decorative icon shown inside the trigger — triggerLabel carries the accessible name, not this. */
  triggerIcon: ReactNode
  items: MenuItem[]
  /** Disables the trigger entirely — e.g. while a previous selection's request is still in flight. */
  disabled?: boolean
  /** Marks the trigger aria-busy — pair with a spinner as triggerIcon while a selection's request is in flight. */
  busy?: boolean
  /**
   * Turns the menu into a set of independent toggles rather than a list
   * of one-shot actions: items become role="menuitemcheckbox", and
   * choosing one leaves the menu open, because the point of a multiple
   * choice is making several of them without reopening in between.
   */
  multi?: boolean
  /**
   * Visible text in the trigger, for a control that has to say what it
   * is currently filtering on. Without it the trigger stays a square
   * glyph — right for the header's chrome, wrong for a filter whose
   * whole job is showing its own state.
   */
  triggerText?: string
  /**
   * Which edge the popup lines up with. 'start' (default) opens it to
   * the right of the trigger; 'end' opens it leftward, which is what a
   * trigger near the viewport's right edge needs — the header menus
   * ran off-screen before this existed.
   */
  align?: 'start' | 'end'
}

/**
 * The checkmark on the currently-selected item of a choice menu. Not
 * exported and not in icons.tsx: it is Menu's own rendering of
 * MenuItem.selected, not a glyph any caller picks.
 */
function CheckMark() {
  return (
    <svg
      viewBox="0 0 16 16"
      width={14}
      height={14}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={styles.check}
    >
      <path d="M3 8.5l3 3 7-8" />
    </svg>
  )
}

/**
 * A pull-down menu primitive: one icon-only trigger button that opens a
 * role="menu" list of role="menuitem" actions — the same interaction as
 * a macOS/iOS pull-down menu (Fase 14 CI-12's reference point). Built by
 * hand rather than reusing Select: a <select> can only ever pick a
 * value, it can't render a per-item icon or fire an action, and its
 * native popup can't be styled at all.
 *
 * Keyboard/focus behavior matches the WAI-ARIA APG "Menu Button"
 * pattern: Enter/Space on the trigger opens the menu via the browser's
 * own click activation; ArrowDown/ArrowUp on the trigger open it and
 * move focus straight to the first/last item; ArrowDown/ArrowUp/Home/End
 * inside the menu move focus between items (arrows wrap); Escape and a
 * click outside both close the menu and return focus to the trigger;
 * choosing an item does the same, after calling its onSelect.
 */
export function Menu({
  triggerLabel,
  triggerIcon,
  items,
  disabled = false,
  busy = false,
  align = 'start',
  multi = false,
  triggerText,
}: MenuProps) {
  const [open, setOpen] = useState(false)
  const focusOnOpenRef = useRef<'first' | 'last'>('first')
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const menuId = useId()

  useEffect(() => {
    if (!open) return

    const menuItems = () =>
      Array.from(
        menuRef.current?.querySelectorAll<HTMLElement>(
          // Every item role this menu can render — miss one and arrow
          // navigation silently stops working for that kind of menu.
          '[role="menuitem"], [role="menuitemradio"], [role="menuitemcheckbox"]',
        ) ?? [],
      )

    const initial = menuItems()
    ;(focusOnOpenRef.current === 'last' ? initial[initial.length - 1] : initial[0])?.focus()

    function closeAndReturnFocus() {
      setOpen(false)
      triggerRef.current?.focus()
    }

    function handlePointerDown(event: MouseEvent) {
      const target = event.target as Node
      if (menuRef.current?.contains(target) || triggerRef.current?.contains(target)) return
      setOpen(false)
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        event.preventDefault()
        closeAndReturnFocus()
        return
      }
      if (event.key === 'Tab') {
        setOpen(false)
        return
      }

      const focusable = menuItems()
      if (focusable.length === 0) return
      const currentIndex = focusable.indexOf(document.activeElement as HTMLElement)

      if (event.key === 'ArrowDown') {
        event.preventDefault()
        focusable[(currentIndex + 1) % focusable.length]!.focus()
      } else if (event.key === 'ArrowUp') {
        event.preventDefault()
        focusable[(currentIndex - 1 + focusable.length) % focusable.length]!.focus()
      } else if (event.key === 'Home') {
        event.preventDefault()
        focusable[0]!.focus()
      } else if (event.key === 'End') {
        event.preventDefault()
        focusable[focusable.length - 1]!.focus()
      }
    }

    document.addEventListener('mousedown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  function handleTriggerKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      // stopPropagation matters here, not just style: this same keydown
      // still bubbles to document after this handler returns, and the
      // effect below (which the setOpen(true) call schedules) attaches
      // its own document-level keydown listener — and focuses the first
      // item — before that bubble reaches document. Without this, that
      // listener sees this exact event a second time and reads it as a
      // navigation step, landing focus one item past the first.
      event.stopPropagation()
      focusOnOpenRef.current = 'first'
      setOpen(true)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      event.stopPropagation()
      focusOnOpenRef.current = 'last'
      setOpen(true)
    }
  }

  function handleSelect(item: MenuItem) {
    // A multiple choice stays open: closing after each toggle would
    // make selecting three things a matter of opening the menu three
    // times. Escape, an outside click or Tab still close it.
    if (!multi) {
      setOpen(false)
      triggerRef.current?.focus()
    }
    item.onSelect()
  }

  return (
    <div className={styles.wrapper}>
      <button
        ref={triggerRef}
        type="button"
        className={`${styles.trigger} ${triggerText ? styles.triggerWide : ''}`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        aria-label={triggerLabel}
        aria-busy={busy || undefined}
        disabled={disabled}
        onClick={() => {
          focusOnOpenRef.current = 'first'
          setOpen((value) => !value)
        }}
        onKeyDown={handleTriggerKeyDown}
      >
        {triggerIcon}
        {triggerText && <span className={styles.triggerText}>{triggerText}</span>}
        {triggerText && (
          <span className={styles.triggerChevron} aria-hidden="true">
            <ChevronDownIcon />
          </span>
        )}
      </button>
      {open && (
        <div
          id={menuId}
          ref={menuRef}
          role="menu"
          aria-label={triggerLabel}
          className={`${styles.menu} ${align === 'end' ? styles.menuAlignEnd : ''}`}
        >
          {items.map((item) => (
            <button
              key={item.key}
              type="button"
              role={
                item.selected === undefined ? 'menuitem' : multi ? 'menuitemcheckbox' : 'menuitemradio'
              }
              aria-checked={item.selected}
              className={styles.menuItem}
              onClick={() => handleSelect(item)}
            >
              {item.icon}
              {item.label}
              {item.selected && <CheckMark />}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
