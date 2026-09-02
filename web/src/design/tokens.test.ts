/// <reference types="node" />
// Node types are referenced here only, not project-wide (tsconfig.app.json's
// "types" deliberately omits "node" — this is browser app code everywhere
// else, and it should stay an error to reach for a Node API by accident).
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// Parses tokens.css itself rather than duplicating the hex values here —
// a copy that could drift from what's actually shipped would make this
// test prove nothing. This is the only thing in the app allowed to read
// tokens.css as text instead of importing it as a stylesheet.
const tokensPath = join(dirname(fileURLToPath(import.meta.url)), 'tokens.css')
const tokensSource = readFileSync(tokensPath, 'utf-8')

function readColorToken(name: string): string {
  const match = tokensSource.match(new RegExp(`--${name}:\\s*(#[0-9a-fA-F]{6})\\s*;`))
  if (!match) {
    throw new Error(`tokens.css has no color custom property named --${name}`)
  }
  return match[1] as string
}

// WCAG 2.x relative luminance / contrast ratio, computed from sRGB hex —
// see https://www.w3.org/TR/WCAG21/#dfn-relative-luminance. Kept
// dependency-free and local rather than pulled from a package: it's a
// dozen lines, and pulling in a library for it would be exactly the kind
// of dependency CLAUDE.md's Go side already avoids adding lightly.
function relativeLuminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
  const [r, g, b] = channels.map((c) =>
    c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4,
  ) as [number, number, number]
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrastRatio(hexA: string, hexB: string): number {
  const luminanceA = relativeLuminance(hexA)
  const luminanceB = relativeLuminance(hexB)
  const [lighter, darker] =
    luminanceA > luminanceB ? [luminanceA, luminanceB] : [luminanceB, luminanceA]
  return (lighter + 0.05) / (darker + 0.05)
}

// WCAG AA. This app has no "large text" use of any of these pairs today
// (headings use text-primary, same as body), so every pair is held to
// the stricter 4.5:1 normal-text threshold rather than 3:1.
const AA_NORMAL_TEXT = 4.5

// Every text/background pair actually used in the app — see tokens.css's
// Color section. Not every possible combination of every color: the
// pairs a design never puts together don't need a contrast guarantee,
// and asserting them would just make this list a lie about what's
// checked versus what's used.
const textOnBackgroundPairs: Array<[name: string, fg: string, bg: string]> = [
  ['text-primary on bg', 'color-text-primary', 'color-bg'],
  ['text-primary on surface', 'color-text-primary', 'color-surface'],
  ['text-secondary on bg', 'color-text-secondary', 'color-bg'],
  ['text-secondary on surface', 'color-text-secondary', 'color-surface'],
  ['on-accent on accent', 'color-on-accent', 'color-accent'],
  ['accent on bg (links)', 'color-accent', 'color-bg'],
  ['accent-hover on bg', 'color-accent-hover', 'color-bg'],
  ['success on bg', 'color-success', 'color-bg'],
  ['warning-text on warning-bg', 'color-warning-text', 'color-warning-bg'],
  ['danger on bg', 'color-danger', 'color-bg'],
  ['on-danger on danger', 'color-on-danger', 'color-danger'],
]

describe('design tokens: color contrast (WCAG AA)', () => {
  it.each(textOnBackgroundPairs)('%s is >= 4.5:1', (_name, fgToken, bgToken) => {
    const fg = readColorToken(fgToken)
    const bg = readColorToken(bgToken)
    const ratio = contrastRatio(fg, bg)
    expect(ratio).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
  })
})

// WCAG 1.4.11 (Non-text Contrast): a UI component's visual boundary needs
// this, not the stricter 4.5:1 normal-text threshold above — see CI-5's
// entry in docs/changes/web-frontend/plan.md for the finding this
// resolves. color-border itself is deliberately excluded (it's divider
// weight, never a control boundary) — see its comment in tokens.css.
const NON_TEXT_UI_CONTRAST = 3

const interactiveBorderPairs: Array<[name: string, fg: string, bg: string]> = [
  ['border-interactive on bg', 'color-border-interactive', 'color-bg'],
  ['border-interactive on surface', 'color-border-interactive', 'color-surface'],
]

describe('design tokens: non-text UI contrast (WCAG 1.4.11)', () => {
  it.each(interactiveBorderPairs)('%s is >= 3:1', (_name, fgToken, bgToken) => {
    const fg = readColorToken(fgToken)
    const bg = readColorToken(bgToken)
    const ratio = contrastRatio(fg, bg)
    expect(ratio).toBeGreaterThanOrEqual(NON_TEXT_UI_CONTRAST)
  })
})
