import { describe, expect, it } from 'vitest'
import { assertOnlyTokens } from './assertOnlyTokens'

describe('assertOnlyTokens', () => {
  it('passes CSS that only references tokens', () => {
    expect(() =>
      assertOnlyTokens(
        `.button { padding: var(--space-2) var(--space-4); color: var(--color-on-accent); border-radius: var(--radius-md); }`,
        'ok.module.css',
      ),
    ).not.toThrow()
  })

  it('allows the small set of legitimately-literal values', () => {
    expect(() =>
      assertOnlyTokens(`.el { margin: 0; padding: auto; top: 50%; left: 100%; }`, 'ok.module.css'),
    ).not.toThrow()
  })

  it('catches a literal hex color', () => {
    expect(() => assertOnlyTokens(`.button { color: #ff0000; }`, 'bad.module.css')).toThrow(
      /literal color/,
    )
  })

  it('catches a literal spacing value', () => {
    expect(() => assertOnlyTokens(`.button { padding: 12px; }`, 'bad.module.css')).toThrow(
      /literal spacing\/radius/,
    )
  })

  it('catches a literal border-radius value', () => {
    expect(() => assertOnlyTokens(`.card { border-radius: 6px; }`, 'bad.module.css')).toThrow(
      /literal spacing\/radius/,
    )
  })

  it('does not mistake border-right-color for the bare "right" position property', () => {
    // Regression: an earlier version matched "right" as a substring of
    // "border-right-color" (word-boundary after the hyphen is still a
    // boundary), flagging an unrelated color property as a missing
    // spacing token.
    expect(() =>
      assertOnlyTokens(`.spinner { border-right-color: transparent; }`, 'ok.module.css'),
    ).not.toThrow()
  })
})
