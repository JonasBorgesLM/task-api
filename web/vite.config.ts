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
    environment: 'node',
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
