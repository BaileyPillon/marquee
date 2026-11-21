import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The Go binary embeds web/dist and serves it from "/", falling back to index.html for
// unknown non-API paths (the SPA uses history-API routes), so assets use absolute URLs.
export default defineConfig({
  base: '/',
  plugins: [svelte()],
  // Dev only (`npm run dev`): the page comes from Vite and /api goes to a running Marquee server.
  // A production build never sees this; the Go binary serves both from one origin.
  server: {
    proxy: { '/api': 'http://127.0.0.1:6465' },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    cssCodeSplit: true,
    sourcemap: false,
    modulePreload: { polyfill: false },
    reportCompressedSize: false,
  },
});
