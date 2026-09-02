/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { Modal } from './Modal'

describe('Modal.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'Modal.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'Modal.module.css')
  })
})

describe('Modal', () => {
  it('renders nothing when closed', () => {
    render(
      <Modal open={false} onClose={vi.fn()} title="Delete task">
        <p>Are you sure?</p>
      </Modal>,
    )
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders as a labelled dialog when open', () => {
    render(
      <Modal open onClose={vi.fn()} title="Delete task">
        <p>Are you sure?</p>
      </Modal>,
    )
    const dialog = screen.getByRole('dialog', { name: 'Delete task' })
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(screen.getByText('Are you sure?')).toBeInTheDocument()
  })

  it('moves focus into the dialog when it opens', () => {
    render(
      <Modal open onClose={vi.fn()} title="Delete task">
        <button type="button">Confirm</button>
      </Modal>,
    )
    expect(screen.getByRole('button', { name: 'Confirm' })).toHaveFocus()
  })

  it('Escape calls onClose', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(
      <Modal open onClose={onClose} title="Delete task">
        <button type="button">Confirm</button>
      </Modal>,
    )

    await user.keyboard('{Escape}')

    expect(onClose).toHaveBeenCalledOnce()
  })

  it('clicking the backdrop calls onClose; clicking inside the dialog does not', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(
      <Modal open onClose={onClose} title="Delete task">
        <p>Are you sure?</p>
      </Modal>,
    )

    await user.click(screen.getByText('Are you sure?'))
    expect(onClose).not.toHaveBeenCalled()

    // The backdrop is the dialog's own parent — role="dialog" itself is
    // rendered inside it, so querying past the dialog reaches it.
    const backdrop = screen.getByRole('dialog').parentElement!
    await user.click(backdrop)
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('Tab cycles focus within the dialog — never escapes to the page behind it', async () => {
    const user = userEvent.setup()
    render(
      <Modal open onClose={vi.fn()} title="Delete task">
        <button type="button">Cancel</button>
        <button type="button">Confirm</button>
      </Modal>,
    )

    const cancel = screen.getByRole('button', { name: 'Cancel' })
    const confirm = screen.getByRole('button', { name: 'Confirm' })

    expect(cancel).toHaveFocus()
    await user.tab()
    expect(confirm).toHaveFocus()
    await user.tab()
    // Wrapped back to the first focusable element, not out of the dialog.
    expect(cancel).toHaveFocus()
    await user.tab({ shift: true })
    expect(confirm).toHaveFocus()
  })

  it('restores focus to the element that was focused before the modal opened', async () => {
    function Harness() {
      const [open, setOpen] = useState(false)
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            Open
          </button>
          <Modal open={open} onClose={() => setOpen(false)} title="Delete task">
            <button type="button" onClick={() => setOpen(false)}>
              Confirm
            </button>
          </Modal>
        </>
      )
    }

    const user = userEvent.setup()
    render(<Harness />)

    const openButton = screen.getByRole('button', { name: 'Open' })
    openButton.focus()
    await user.click(openButton)
    expect(screen.getByRole('button', { name: 'Confirm' })).toHaveFocus()

    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(openButton).toHaveFocus()
  })
})
