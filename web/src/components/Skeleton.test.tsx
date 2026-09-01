/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { Skeleton } from './Skeleton'

describe('Skeleton.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'Skeleton.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'Skeleton.module.css')
  })
})

describe('Skeleton', () => {
  it('is aria-hidden — purely decorative, never its own accessible element', () => {
    const { container } = render(<Skeleton width="100%" height="1rem" />)
    const el = container.firstChild as HTMLElement
    expect(el).toHaveAttribute('aria-hidden', 'true')
  })

  it('applies the requested width/height as inline dimensions', () => {
    const { container } = render(<Skeleton width={240} height="2rem" />)
    const el = container.firstChild as HTMLElement
    expect(el.style.width).toBe('240px')
    expect(el.style.height).toBe('2rem')
  })

  it('applies radius via a token reference, never a literal value', () => {
    const { container } = render(<Skeleton radius="full" />)
    const el = container.firstChild as HTMLElement
    expect(el.style.borderRadius).toBe('var(--radius-full)')
  })
})
