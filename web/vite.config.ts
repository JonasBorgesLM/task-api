/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  test: {
    // No `globals: true`: tests import describe/it/expect explicitly from
    // 'vitest', the same way any other module dependency is imported —
    // consistent with this project's strict TypeScript config, which
    // would otherwise need a separate ambient-globals allowance.
    //
    // jsdom, not node: CI-5's primitives are tested with React Testing
    // Library, which needs a DOM. Node-only tests (tokens.test.ts,
    // errors.test.ts) still work unchanged under jsdom — it only adds
    // browser globals on top of the same Node process, it doesn't
    // remove fs/path/etc.
    environment: 'jsdom',
    setupFiles: ['./src/test-utils/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
