import { svelte } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';

// Kept separate from vite.config.js so the build-only plugins (tailwind,
// woff stripping) don't run under test. The svelte plugin is included so
// `.svelte.ts` rune modules compile when imported by a test.
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
