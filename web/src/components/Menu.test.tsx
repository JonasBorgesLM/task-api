/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { Menu } from './Menu'
import type { MenuItem } from './Menu'

describe('Menu.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'Menu.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'Menu.module.css')
  })
})

function makeItems(onSelect: (key: string) => void): MenuItem[] {
  return [
    { key: 'a', label: 'Item A', onSelect: () => onSelect('a') },
    { key: 'b', label: 'Item B', onSelect: () => onSelect('b') },
    { key: 'c', label: 'Item C', onSelect: () => onSelect('c') },
  ]
}

describe('Menu', () => {
  it('renders a labelled, collapsed trigger and no menu until opened', () => {
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} />)

    const trigger = screen.getByRole('button', { name: 'Actions' })
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu')
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('clicking the trigger opens the menu, lists every item, and moves focus to the first one', async () => {
    const user = userEvent.setup()
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} />)

    await user.click(screen.getByRole('button', { name: 'Actions' }))

    expect(screen.getByRole('button', { name: 'Actions' })).toHaveAttribute('aria-expanded', 'true')
    const menu = screen.getByRole('menu', { name: 'Actions' })
    const menuItems = screen.getAllByRole('menuitem')
    expect(menuItems.map((el) => el.textContent)).toEqual(['Item A', 'Item B', 'Item C'])
    expect(menu).toBeInTheDocument()
    expect(menuItems[0]).toHaveFocus()
  })

  it('ArrowDown/ArrowUp move focus between items, wrapping at both ends', async () => {
    const user = userEvent.setup()
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} />)
    await user.click(screen.getByRole('button', { name: 'Actions' }))
    const [first, second, third] = screen.getAllByRole('menuitem')

    expect(first).toHaveFocus()
    await user.keyboard('{ArrowDown}')
    expect(second).toHaveFocus()
    await user.keyboard('{ArrowDown}')
    expect(third).toHaveFocus()
    await user.keyboard('{ArrowDown}')
    expect(first).toHaveFocus()
    await user.keyboard('{ArrowUp}')
    expect(third).toHaveFocus()
  })

  it('ArrowUp on the trigger opens the menu with focus on the last item', async () => {
    const user = userEvent.setup()
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} />)
    screen.getByRole('button', { name: 'Actions' }).focus()

    await user.keyboard('{ArrowUp}')

    const menuItems = screen.getAllByRole('menuitem')
    expect(menuItems[menuItems.length - 1]).toHaveFocus()
  })

  it('Home/End jump to the first/last item', async () => {
    const user = userEvent.setup()
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} />)
    await user.click(screen.getByRole('button', { name: 'Actions' }))
    const [first, , third] = screen.getAllByRole('menuitem')

    await user.keyboard('{End}')
    expect(third).toHaveFocus()
    await user.keyboard('{Home}')
    expect(first).toHaveFocus()
  })

  // Regression coverage for a real bug this jsdom suite did NOT catch:
  // the same ArrowDown keydown that opens the menu also bubbles to the
  // document-level navigation listener the opening effect attaches,
  // which — without stopPropagation on the trigger's handler — read it
  // as a second "move to next" step and landed focus one item past the
  // first. jsdom's synthetic event timing didn't reproduce this (this
  // test passed before the fix); it was only caught by a real browser
  // via `vite preview` per plan.md's CI-12 verification requirement.
  // Kept here anyway as a behavioral spec, not a guarantee it alone
  // would catch a regression.
  it('ArrowDown on the trigger opens the menu with focus on the first item, not the second', async () => {
    const user = userEvent.setup()
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} />)
    screen.getByRole('button', { name: 'Actions' }).focus()

    await user.keyboard('{ArrowDown}')

    const menuItems = screen.getAllByRole('menuitem')
    expect(menuItems[0]).toHaveFocus()
  })

  it('clicking an item calls its onSelect, closes the menu, and returns focus to the trigger', async () => {
    const onSelect = vi.fn()
    const user = userEvent.setup()
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(onSelect)} />)
    const trigger = screen.getByRole('button', { name: 'Actions' })
    await user.click(trigger)

    await user.click(screen.getByRole('menuitem', { name: 'Item B' }))

    expect(onSelect).toHaveBeenCalledWith('b')
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
  })

  it('Escape closes the menu and returns focus to the trigger', async () => {
    const user = userEvent.setup()
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} />)
    const trigger = screen.getByRole('button', { name: 'Actions' })
    await user.click(trigger)

    await user.keyboard('{Escape}')

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
  })

  it('a click outside the menu closes it', async () => {
    const user = userEvent.setup()
    render(
      <div>
        <Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} />
        <button type="button">Elsewhere</button>
      </div>,
    )
    await user.click(screen.getByRole('button', { name: 'Actions' }))
    expect(screen.getByRole('menu')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Elsewhere' }))

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('a disabled trigger cannot be opened', async () => {
    const user = userEvent.setup()
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} disabled />)

    await user.click(screen.getByRole('button', { name: 'Actions' }))

    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('busy marks the trigger aria-busy', () => {
    render(<Menu triggerLabel="Actions" triggerIcon={<span>i</span>} items={makeItems(vi.fn())} busy />)

    expect(screen.getByRole('button', { name: 'Actions' })).toHaveAttribute('aria-busy', 'true')
  })
})
