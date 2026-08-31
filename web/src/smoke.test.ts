import { describe, expect, it } from 'vitest'

// CI-2 of docs/changes/web-frontend/plan.md: this test exists only to
// prove the Vitest pipeline actually runs in CI, not to test anything
// real. Delete it once CI-3+ adds tests that exercise real code.
describe('web-ci smoke test', () => {
  it('runs', () => {
    expect(1 + 1).toBe(2)
  })
})
