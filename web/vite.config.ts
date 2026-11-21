import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The Go binary embeds web/dist and serves it from "/", falling back to index.html for
// unknown non-API paths (the SPA uses history-API routes), so assets use absolute URLs.
export default defineConfig({
  base: '/',
  plugins: [svelte()],
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
