// Mirrors internal/user/password_strength.go's checks exactly — issue
// #219's own framing is "espelha a regra do servidor e nunca inventa uma
// mais frouxa" (mirrors the server's rule, never invents a looser one).
// This project has no cross-language codegen for a shared rule like
// this one, so the two files are kept in sync by hand: if the Go list or
// checks change, this file changes with them in the same commit — see
// docs/DECISIONS.md § "Validação de senha forte".

// Same ~150 entries as commonWeakPasswords in password_strength.go,
// lowercase. "password123" is deliberately absent there for the same
// reason it would be absent here: it is this project's own established
// demo/test password (registerAndLogin in web/e2e/fixtures.ts and every
// RegisterPage test use "correct horse battery staple" instead, so this
// omission has no effect on this app's own test fixtures either way).
const COMMON_WEAK_PASSWORDS = new Set([
  '123456', 'password', '12345678', 'qwerty', '123456789', '12345',
  '1234', '111111', '1234567', 'dragon', '123123', 'baseball',
  'iloveyou', 'trustno1', '1234567890', 'sunshine', 'master', '123321',
  'letmein', '696969', 'shadow', 'michael', 'superman', '1qaz2wsx',
  '7777777', '121212', '000000', 'qazwsx', '123qwe', 'killer',
  'jordan', 'jennifer', 'hunter', 'buster', 'soccer', 'harley',
  'batman', 'andrew', 'tigger', 'charlie', 'robert', 'thomas',
  'hockey', 'ranger', 'daniel', 'starwars', '112233', 'george',
  'computer', 'michelle', 'jessica', 'pepper', '1111', 'zxcvbn',
  '555555', '11111111', '131313', 'freedom', '777777', 'pass',
  'maggie', '159753', 'aaaaaa', 'ginger', 'princess', 'joshua',
  'cheese', 'amanda', 'summer', 'ashley', '6969', 'nicole',
  'chelsea', 'biteme', 'matthew', 'access', 'yankees', '987654321',
  'dallas', 'austin', 'thunder', 'taylor', 'matrix', 'mustang',
  'ninja', 'welcome', 'welcome1', 'admin', 'administrator', 'admin123',
  'root', 'toor', 'changeme', 'letmein1', 'password1',
  'passw0rd', 'p@ssw0rd', 'passw0rd!', 'password1!', 'qwerty123',
  'qwertyuiop', '1q2w3e4r', '1q2w3e4r5t', 'zaq1zaq1', 'aa123456',
  'asdfghjkl', 'abc12345', 'abcd1234', '1qaz2wsx3edc', 'iloveyou1',
  'whatever', 'trustme', 'letmein123', 'changeme123', 'temppass',
  'temp123', 'guest', 'guest123', 'test123', 'testing', 'default',
  'default123', 'incorrect', 'wordpass', 'mypassword', 'mypassword1',
  'hello123', 'hello1', 'welcome123', 'abc123', 'monkey', 'football',
  'jesus', 'flower', 'hottie', 'loveme', 'biteme1', 'sunshine1',
])

/** Mirrors isCommonWeakPassword in password_strength.go. */
export function isCommonWeakPassword(password: string): boolean {
  return COMMON_WEAK_PASSWORDS.has(password.toLowerCase())
}

/**
 * Mirrors isSingleRepeatedRune in password_strength.go. Array.from
 * splits by Unicode code point (not UTF-16 code unit), the same
 * granularity Go's []rune conversion gives the server-side check.
 */
export function isSingleRepeatedRune(password: string): boolean {
  const chars = Array.from(password)
  return chars.every((c) => c === chars[0])
}

/** Mirrors isSequentialRun in password_strength.go. */
export function isSequentialRun(password: string): boolean {
  const codePoints = Array.from(password).map((c) => c.codePointAt(0)!)
  if (codePoints.length < 2) return false
  let ascending = true
  let descending = true
  for (let i = 1; i < codePoints.length; i++) {
    // Non-null: i ranges over [1, codePoints.length), so both indices
    // are always in bounds — noUncheckedIndexedAccess can't infer that
    // from a numeric loop bound the way it can from destructuring.
    const delta = codePoints[i]! - codePoints[i - 1]!
    if (delta !== 1) ascending = false
    if (delta !== -1) descending = false
  }
  return ascending || descending
}

export interface PasswordRequirement {
  id: string
  label: string
  met: (password: string) => boolean
}

/**
 * The requirements PasswordRequirements.tsx renders as a live checklist.
 * Deliberately a checklist against the server's own predicates, not a
 * weak/fair/strong score — a score is exactly the kind of thing that
 * could tell a user their password is fine when the server would still
 * reject it. minPasswordLen/maxPasswordLen are mirrored as 8/72 directly
 * (not imported — there is nothing to import from across a language
 * boundary); the 72-character ceiling isn't listed here since it isn't a
 * strength concern, the same reasoning validatePassword's Go doc comment
 * gives.
 */
export const PASSWORD_REQUIREMENTS: PasswordRequirement[] = [
  { id: 'length', label: 'At least 8 characters', met: (p) => p.length >= 8 },
  {
    id: 'not-common',
    label: 'Not a commonly used password',
    met: (p) => !isCommonWeakPassword(p),
  },
  {
    id: 'not-predictable',
    label: 'Not a repeated or sequential pattern',
    met: (p) => !isSingleRepeatedRune(p) && !isSequentialRun(p),
  },
]
