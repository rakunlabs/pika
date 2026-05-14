import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { readFileSync, writeFileSync, readdirSync, unlinkSync, statSync } from 'node:fs';
import { join } from 'node:path';

/**
 * Strip the legacy `.woff` fallback from every @font-face rule in
 * the final bundle, then delete the orphaned .woff asset files.
 *
 * Each @fontsource face ships with both `.woff2` (modern, smaller,
 * natively compressed) and `.woff` (legacy fallback for IE11 and
 * Safari < 12). Modern browsers — every version of Chrome, Firefox,
 * Safari, and Edge we target — support woff2 since ~2018. Keeping
 * the woff fallback in the CSS makes Rollup ship every legacy file
 * (~900 KB of orphan assets in dist/assets/) even though no live
 * browser ever downloads them.
 *
 * We do this in `closeBundle` rather than in `transform`/`generateBundle`
 * because Tailwind v4's CSS pipeline resolves `@import` of @fontsource
 * CSS before Vite's transform hook sees the rule — so a transform-time
 * regex never matches. Post-build mutation of the emitted CSS works
 * reliably because by that point every URL has been rewritten to its
 * final hashed filename and the @font-face rule sits in plain text.
 *
 * The regex is intentionally narrow: it requires an existing `woff2`
 * clause in front so we never drop a face that's woff-only (none
 * exist in our set today, but a future addition shouldn't quietly
 * lose its only format).
 */
const stripWoffFallback = {
  name: 'pika:strip-woff-fallback',
  apply: 'build',
  closeBundle() {
    // Vite default output dir; the Makefile moves this to
    // `internal/server/dist/` later. We rewrite + prune here so
    // both the dev preview (`_ui/dist`) and the released bundle
    // are clean.
    const outDir = './dist/assets';
    let dir;
    try {
      dir = readdirSync(outDir);
    } catch {
      // Build may have emitted somewhere else (different config)
      // — fall back to a no-op rather than failing the build.
      return;
    }

    // First pass: rewrite every CSS file, collect the set of woff
    // filenames the modified CSS no longer references.
    const referenced = new Set();
    for (const name of dir) {
      if (!name.endsWith('.css')) continue;
      const path = join(outDir, name);
      const original = readFileSync(path, 'utf8');
      const stripped = original.replace(
        /(url\([^)]+\.woff2\)\s*format\(['"]woff2['"]\))\s*,\s*url\([^)]+\.woff\)\s*format\(['"]woff['"]\)/g,
        '$1',
      );
      if (stripped !== original) writeFileSync(path, stripped);
      // After stripping, any .woff URLs still left in the file are
      // legitimate references we shouldn't delete the asset for.
      for (const m of stripped.matchAll(/[A-Za-z0-9._-]+\.woff\b/g)) {
        referenced.add(m[0]);
      }
    }

    // Second pass: delete every orphaned .woff in the assets dir.
    let removed = 0;
    let bytes = 0;
    for (const name of dir) {
      if (!name.endsWith('.woff')) continue;
      if (referenced.has(name)) continue;
      const path = join(outDir, name);
      try {
        bytes += statSync(path).size;
        unlinkSync(path);
        removed++;
      } catch {
        // Ignore — file may have been cleaned up by another tool.
      }
    }
    if (removed > 0) {
      // eslint-disable-next-line no-console
      console.log(`[strip-woff] removed ${removed} legacy .woff files (${(bytes / 1024).toFixed(0)} KB)`);
    }
  },
};

export default defineConfig({
  base: './',
  plugins: [
    tailwindcss(),
    svelte(),
    stripWoffFallback,
  ],
  resolve: {
    alias: {
      '@': '/src'
    }
  },
  server: {
    proxy: {
      '^/(api|data|raw|login)/': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: true,
        ws: true,
        followRedirects: true
      },
      '^/logout$': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: true
      }
    },
    port: 3000
  }
});
