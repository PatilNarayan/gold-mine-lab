/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'node:path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    // Tests always run against the deterministic mock services — a developer's
    // local .env (e.g. VITE_DATA_MODE=api) must not leak into the suite.
    env: { VITE_DATA_MODE: 'mock' },
  },
});
