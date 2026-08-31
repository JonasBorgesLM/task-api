import { cleanup } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'

// RTL's auto-cleanup normally hooks a global afterEach (Jest-style).
// This project deliberately doesn't set `globals: true` (see
// vite.config.ts) — tests import describe/it/expect explicitly — so
// nothing wires that hook automatically. Without this, every render()
// across every test in a file accumulates in the same jsdom document
// and later tests start seeing duplicate elements from earlier ones.
afterEach(() => {
  cleanup()
})
