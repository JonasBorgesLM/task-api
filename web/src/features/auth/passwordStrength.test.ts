import { describe, expect, it } from 'vitest'
import {
  isCommonWeakPassword,
  isSequentialRun,
  isSingleRepeatedRune,
  PASSWORD_REQUIREMENTS,
} from './passwordStrength'

describe('isCommonWeakPassword', () => {
  it('matches an exact entry', () => {
    expect(isCommonWeakPassword('welcome1')).toBe(true)
  })

  it('matches case-insensitively', () => {
    expect(isCommonWeakPassword('WELCOME1')).toBe(true)
    expect(isCommonWeakPassword('Password1!')).toBe(true)
  })

  it('does not match an uncommon password', () => {
    expect(isCommonWeakPassword('correct horse battery staple')).toBe(false)
  })

  it('deliberately does not list password123 — see the file-level comment', () => {
    expect(isCommonWeakPassword('password123')).toBe(false)
  })
})

describe('isSingleRepeatedRune', () => {
  it('is true for the same character repeated', () => {
    expect(isSingleRepeatedRune('aaaaaaaa')).toBe(true)
    expect(isSingleRepeatedRune('11111111')).toBe(true)
  })

  it('is false once a different character appears', () => {
    expect(isSingleRepeatedRune('aaaaaaab')).toBe(false)
  })

  it('is false for an ordinary password', () => {
    expect(isSingleRepeatedRune('correct horse battery staple')).toBe(false)
  })
})

describe('isSequentialRun', () => {
  it('is true for an ascending or descending run', () => {
    expect(isSequentialRun('12345678')).toBe(true)
    expect(isSequentialRun('87654321')).toBe(true)
    expect(isSequentialRun('abcdefgh')).toBe(true)
  })

  it('is false for a broken run', () => {
    expect(isSequentialRun('12345679')).toBe(false)
  })

  it('is false for a repeated (non-sequential) pattern', () => {
    expect(isSequentialRun('aaaaaaaa')).toBe(false)
  })

  it('is false for an ordinary password', () => {
    expect(isSequentialRun('correct horse battery staple')).toBe(false)
  })
})

describe('PASSWORD_REQUIREMENTS', () => {
  it('every requirement is met by a genuinely strong password', () => {
    for (const requirement of PASSWORD_REQUIREMENTS) {
      expect(requirement.met('correct horse battery staple')).toBe(true)
    }
  })

  it('the length requirement alone fails a short password', () => {
    const length = PASSWORD_REQUIREMENTS.find((r) => r.id === 'length')!
    expect(length.met('short1')).toBe(false)
  })

  it('the not-common requirement alone fails a blocklisted password', () => {
    const notCommon = PASSWORD_REQUIREMENTS.find((r) => r.id === 'not-common')!
    expect(notCommon.met('welcome1')).toBe(false)
  })

  it('the not-predictable requirement alone fails a sequential password', () => {
    const notPredictable = PASSWORD_REQUIREMENTS.find((r) => r.id === 'not-predictable')!
    expect(notPredictable.met('12345678')).toBe(false)
  })
})
