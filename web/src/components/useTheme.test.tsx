import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useTheme } from './useTheme'

function dataTheme(): string | null {
  return document.documentElement.getAttribute('data-theme')
}

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  afterEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  it('defaults to "system" — no explicit choice, no data-theme attribute — when nothing is stored', () => {
    const { result } = renderHook(() => useTheme())

    expect(result.current.preference).toBe('system')
    // tokens.css's own @media rule handles "system" with zero JS
    // involvement; the absence of the attribute is what lets it.
    expect(dataTheme()).toBeNull()
  })

  it('choosing "dark" sets data-theme and persists it', () => {
    const { result } = renderHook(() => useTheme())

    act(() => result.current.setPreference('dark'))

    expect(result.current.preference).toBe('dark')
    expect(dataTheme()).toBe('dark')
    expect(localStorage.getItem('theme')).toBe('dark')
  })

  it('choosing "light" sets data-theme and persists it', () => {
    const { result } = renderHook(() => useTheme())

    act(() => result.current.setPreference('light'))

    expect(result.current.preference).toBe('light')
    expect(dataTheme()).toBe('light')
    expect(localStorage.getItem('theme')).toBe('light')
  })

  it('choosing "system" again after an explicit choice removes the attribute and clears storage', () => {
    const { result } = renderHook(() => useTheme())

    act(() => result.current.setPreference('dark'))
    expect(dataTheme()).toBe('dark')

    act(() => result.current.setPreference('system'))

    expect(result.current.preference).toBe('system')
    expect(dataTheme()).toBeNull()
    expect(localStorage.getItem('theme')).toBeNull()
  })

  it('picks up a previously-stored explicit choice on mount, matching what index.html already applied before React ran', () => {
    localStorage.setItem('theme', 'dark')

    const { result } = renderHook(() => useTheme())

    expect(result.current.preference).toBe('dark')
    expect(dataTheme()).toBe('dark')
  })

  it('ignores a garbage stored value and falls back to "system"', () => {
    localStorage.setItem('theme', 'sepia')

    const { result } = renderHook(() => useTheme())

    expect(result.current.preference).toBe('system')
    expect(dataTheme()).toBeNull()
  })
})
