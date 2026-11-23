import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

import { previewPages } from './scripts/previews.ts';

// `pnpm --dir web build:design`: the preview pages and dist/styles.css. The app's own build is
// web/vite.config.ts; this one never touches web/dist.
export default defineConfig({
  plugins: [react(), tailwindcss(), previewPages()],
});
