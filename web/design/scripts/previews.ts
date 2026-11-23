// Vite plugin for `pnpm --dir web build:design`: turns every .design-sync/previews/<Name>.tsx into
// dist/previews/<Name>.html, an HTML page whose first line is the file's
// `<!-- @dsCard group="..." -->` marker (the Claude Design pane groups cards by it), plus
// dist/previews/index.html and dist/styles.css (Tailwind, the tokens and every component class:
// design-sync's cssEntry).
//
// Generated sources go to .preview-build/ (git-ignored). The output has no timestamps.

import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { basename, join } from 'node:path';

import type { Plugin } from 'vite';

const design = join(import.meta.dirname, '..');
export const previewsDir = join(design, '.design-sync/previews');
const work = join(design, '.preview-build');
export const outDir = join(design, 'dist');

/** `// @dsCard group="Forms"`, the first line of every preview file. */
export const SOURCE_MARKER = /^\/\/ @dsCard group="([^"]{1,64})"$/;

export interface Preview {
  name: string;
  group: string;
}

export function discoverPreviews(): Preview[] {
  return readdirSync(previewsDir)
    .filter((file) => file.endsWith('.tsx'))
    .sort()
    .map((file) => {
      const first = readFileSync(join(previewsDir, file), 'utf8').split('\n', 1)[0] ?? '';
      const group = SOURCE_MARKER.exec(first)?.[1];
      if (!group) throw new Error(`${file}: the first line must be // @dsCard group="<Group>"`);
      return { name: basename(file, '.tsx'), group };
    });
}

const escape = (s: string) => s.replace(/[&<>"]/g, (c) => `&#${String(c.charCodeAt(0))};`);

function page(group: string, title: string, script: string): string {
  return `<!-- @dsCard group="${escape(group)}" -->
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>${escape(title)} · design system</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="./${script}"></script>
  </body>
</html>
`;
}

export function previewPages(): Plugin {
  return {
    name: 'design-previews',
    config() {
      const previews = discoverPreviews();
      if (previews.length === 0) throw new Error('No previews in .design-sync/previews');
      rmSync(work, { recursive: true, force: true });
      mkdirSync(join(work, 'previews'), { recursive: true });

      // source(none): only the design system's own files feed dist/styles.css.
      writeFileSync(
        join(work, 'styles.css'),
        `@import 'tailwindcss' source(none);
@import '../src/styles.css';
@source '../.design-sync/previews';
@source '../scripts';
`,
      );
      const input: Record<string, string> = { styles: join(work, 'styles.css') };

      for (const { name, group } of previews) {
        const entry = `${name}.entry.tsx`;
        writeFileSync(
          join(work, 'previews', entry),
          `import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import * as stories from '../../.design-sync/previews/${name}';
import { Stories } from '../../scripts/harness';
import '../styles.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Stories name=${JSON.stringify(name)} stories={stories} />
  </StrictMode>,
);
`,
        );
        writeFileSync(join(work, 'previews', `${name}.html`), page(group, name, entry));
        input[name] = join(work, 'previews', `${name}.html`);
      }

      const groups = [...new Set(previews.map((p) => p.group))].sort();
      const list = groups
        .map(
          (g) =>
            `<h2 class="mt-6 font-semibold">${escape(g)}</h2><ul class="mt-2 list-disc pl-6">${previews
              .filter((p) => p.group === g)
              .map((p) => `<li><a class="underline" href="./${p.name}.html">${escape(p.name)}</a></li>`)
              .join('')}</ul>`,
        )
        .join('\n');
      writeFileSync(join(work, 'previews', 'index.ts'), "import '../styles.css';\n");
      writeFileSync(
        join(work, 'previews', 'index.html'),
        page('Overview', 'Previews', 'index.ts').replace(
          '<div id="root"></div>',
          `<main class="mx-auto max-w-3xl p-6"><h1 class="text-xl font-semibold">Previews</h1>\n${list}</main>`,
        ),
      );
      input.index = join(work, 'previews', 'index.html');

      return {
        root: work,
        base: './',
        logLevel: 'warn',
        build: {
          outDir,
          emptyOutDir: true,
          assetsInlineLimit: 0,
          sourcemap: false,
          rolldownOptions: {
            input,
            output: {
              assetFileNames: (asset) =>
                asset.names.includes('styles.css') ? 'styles.css' : 'previews/assets/[name]-[hash][extname]',
              chunkFileNames: 'previews/assets/[name]-[hash].js',
              entryFileNames: 'previews/assets/[name]-[hash].js',
            },
          },
        },
      };
    },
  };
}
