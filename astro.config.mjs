import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import { readSnapshot } from './scripts/audit-build.mjs';
import { accessSync } from 'node:fs';

readSnapshot(process.env.CATALOG_SNAPSHOT);
if (!process.env.CATALOG_MEDIA_DIR || !process.env.BUILD_SITE_DIR) throw new Error('CATALOG_MEDIA_DIR and BUILD_SITE_DIR are required');
accessSync(process.env.CATALOG_MEDIA_DIR);

export default defineConfig({
  site: process.env.SITE_URL || process.env.CF_PAGES_URL || 'https://yuanfa-tools.pages.dev',
  output: 'static',
  outDir: process.env.BUILD_SITE_DIR,
  cacheDir: './.build-cache',
  integrations: [react()],
  trailingSlash: 'always',
  compressHTML: true,
  build: {
    format: 'directory',
    inlineStylesheets: 'auto',
  },
  server: {
    host: '127.0.0.1',
    port: 4330,
    // Astro preview reuses this list for its Vite host check, so tunnel hosts work.
    allowedHosts: ['.trycloudflare.com'],
  },
  vite: {
    cacheDir: './.build-cache/vite',
    server: {
      allowedHosts: ['.trycloudflare.com'],
    },
  },
});
