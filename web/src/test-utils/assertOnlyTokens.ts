// Shared by every primitive's *.test.tsx — CI-5 of
// docs/changes/web-frontend/plan.md requires "nenhum valor de cor/spacing
// fora de tokens.css". Reads the component's own .module.css source (the
// same real-file-parsing approach tokens.test.ts uses for tokens.css
// itself) and fails if it finds a color or spacing/radius value that
// isn't a var(--...) reference.

const HEX_COLOR = /#[0-9a-fA-F]{3,8}\b/g

// Properties whose value should always be a spacing/radius token. Only
// these — not every property — because a blanket "no literal number"
// rule would flag legitimate non-token values (line-height: 1, flex: 1,
// z-index: 10) that have nothing to do with tokens.css at all.
//
// Matched with matchAll rather than a per-line, ^-anchored scan: CSS is
// free to put more than one declaration on a line (or a whole rule on
// one line, as some of this file's own test fixtures do), and an anchor
// would silently stop checking anything not at the start of its line.
//
// The (?<![\w-]) lookbehind (rather than \b) matters specifically for
// top/right/bottom/left: \b alone still matches "right" inside
// "border-right-color" (there's a word boundary right after the hyphen),
// which would flag an unrelated property as a bare positioning one. The
// lookbehind additionally rejects a hyphen immediately before the match.
const SPACING_DECLARATION =
  /(?<![\w-])(padding|margin|gap|row-gap|column-gap|border-radius|top|right|bottom|left)(-\w+)?\s*:\s*([^;{}]+);/g

// Values that are legitimately literal even on a spacing property — they
// don't correspond to any tokens.css scale value, so requiring var() for
// them would be requiring a token that doesn't exist.
const ALLOWED_LITERAL_VALUES = new Set(['0', 'auto', '50%', '100%'])

export function assertOnlyTokens(cssSource: string, fileName: string): void {
  const hexMatches = cssSource.match(HEX_COLOR)
  if (hexMatches) {
    throw new Error(
      `${fileName} has a literal color value (${hexMatches.join(', ')}) — use var(--color-*) from tokens.css instead`,
    )
  }

  for (const match of cssSource.matchAll(SPACING_DECLARATION)) {
    const value = match[3]!.trim()
    if (value.includes('var(') || ALLOWED_LITERAL_VALUES.has(value)) continue
    throw new Error(
      `${fileName} has a literal spacing/radius value ("${match[0]!.trim()}") — use var(--space-*)/var(--radius-*) from tokens.css instead`,
    )
  }
}
