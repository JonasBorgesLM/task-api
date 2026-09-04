/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { assertOnlyTokens } from '../test-utils/assertOnlyTokens'
import { PageContainer } from './PageContainer'

describe('PageContainer.module.css', () => {
  it('uses only design tokens, no literal color/spacing', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'PageContainer.module.css')
    assertOnlyTokens(readFileSync(cssPath, 'utf-8'), 'PageContainer.module.css')
  })
})

describe('PageContainer', () => {
  it('renders its children, unaltered', () => {
    render(
      <PageContainer>
        <h1>Page title</h1>
      </PageContainer>,
    )
    expect(screen.getByRole('heading', { name: 'Page title' })).toBeInTheDocument()
  })
})
