import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// In development Vite serves the app on :5173 and forwards /api to the Go server, so the browser
// sees one origin, as in production where the Go binary serves both.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': `http://127.0.0.1:${process.env.API_PORT ?? '8080'}`,
    },
  },
  build: {
    // No inline scripts or styles, so the strict CSP holds; no source maps shipped.
    assetsInlineLimit: 0,
    sourcemap: false,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}', 'design/src/**/*.test.{ts,tsx}'],
  },
});
