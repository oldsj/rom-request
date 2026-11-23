// `pnpm --dir web check:design`, after `pnpm --dir web build:design`. Fails (exit 1) when:
//   - a component exported from src/index.ts has no .design-sync/previews/<Name>.tsx;
//   - a preview's first line is not its `// @dsCard group="..."` marker, or its built page's first
//     line is not the matching `<!-- @dsCard group="..." -->`;
//   - a theme in src/tokens.css is missing a colour the default theme defines, or adds a token;
//   - the repository's DESIGN.md lists a colour that differs from src/tokens.css, or misses one;
//   - a component, preview or app file uses a raw colour (hex, rgb(), oklch() ...), a Tailwind
//     palette colour (bg-stone-100), a `dark:` variant (tokens switch themes, not classes), or an
//     opacity modifier (bg-primary/50: color-mix() needs newer engines than the rest);
//   - axe finds a serious or critical WCAG 2.2 A/AA violation on any preview page, with the OS in light
//     or dark mode, or a page throws.
// Needs Chromium: `pnpm --dir web exec playwright install chromium` once.

import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';

import AxeBuilder from '@axe-core/playwright';
import { chromium } from '@playwright/test';
import { preview } from 'vite';

import { discoverPreviews, outDir, previewsDir } from './previews.ts';

const design = join(import.meta.dirname, '..');
const web = join(design, '..');
const problems: string[] = [];

// ---- Every exported component has a preview ---------------------------------------------------

const previews = discoverPreviews();
const index = readFileSync(join(design, 'src/index.ts'), 'utf8');
const components = [...index.matchAll(/export \{([^}]*)\}/g)]
  .flatMap((m) => m[1].split(','))
  .map((s) => s.trim())
  .filter((s) => /^[A-Z]\w*$/.test(s));
for (const name of components) {
  if (!previews.some((p) => p.name === name)) problems.push(`${name}: no .design-sync/previews/${name}.tsx`);
}

// ---- Every theme defines the same tokens ------------------------------------------------------

const tokens = readFileSync(join(design, 'src/tokens.css'), 'utf8');
const blocks = [...tokens.matchAll(/([^{}]*)\{([^{}]*--[^{}]*)\}/g)].map((m) => ({
  selector: m[1].trim().split('\n').at(-1)?.trim() ?? '',
  values: new Map([...m[2].matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)].map((k) => [k[1], k[2].trim()])),
}));
const base = blocks.at(0);
if (!base) problems.push('src/tokens.css: no token blocks');
// Shape and type are defined once, on :root. Every theme redefines every colour, and nothing else.
const colours = [...(base?.values ?? new Map<string, string>())].filter(([, v]) => v.startsWith('#')).map(([k]) => k);
for (const block of blocks.slice(1)) {
  const missing = colours.filter((k) => !block.values.has(k));
  const extra = [...block.values.keys()].filter((k) => !colours.includes(k));
  if (missing.length) problems.push(`src/tokens.css ${block.selector}: missing ${missing.join(', ')}`);
  if (extra.length) problems.push(`src/tokens.css ${block.selector}: not a :root colour: ${extra.join(', ')}`);
}

// ---- DESIGN.md's front matter mirrors the tokens (when the repository has one) ---------------

const designDoc = join(web, '..', 'DESIGN.md');
if (existsSync(designDoc)) {
  const front = /^---\n([\s\S]*?)\n---/.exec(readFileSync(designDoc, 'utf8'))?.[1] ?? '';
  const documented = new Map([...front.matchAll(/^ {2}([a-z][\w-]*): "(#[0-9a-fA-F]{3,8})"$/gm)].map((m) => [m[1], m[2]]));
  const defined = base?.values ?? new Map<string, string>();
  for (const [key, value] of defined) {
    if (!value.startsWith('#')) continue;
    const doc = documented.get(key.slice(2));
    if (doc?.toLowerCase() !== value.toLowerCase()) problems.push(`DESIGN.md colors.${key.slice(2)}: ${doc ?? 'missing'}, tokens.css has ${value}`);
  }
  for (const key of documented.keys()) {
    if (!defined.has(`--${key}`)) problems.push(`DESIGN.md colors.${key}: not a token in src/tokens.css`);
  }
}

// ---- Tokens only: no raw or palette colours outside tokens.css ---------------------------------

const PALETTE =
  'slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|black|white';
const UTILITIES = 'bg|text|border(?:-[xytrblse])?|outline|ring(?:-offset)?|fill|stroke|divide|placeholder|decoration|shadow|accent|caret|from|via|to';
const rules: [RegExp, string][] = [
  [/#[0-9a-fA-F]{3,8}\b/, 'raw hex colour: use a token'],
  [/\b(?:rgba?|hsla?|oklch|oklab|lab|lch|color)\(/, 'raw colour function: use a token'],
  // A colour utility naming a palette colour (bg-stone-100, text-white); token names such as
  // --profile-sky-soft or oc-profile--rose are not utilities and pass.
  [
    new RegExp(`(?:^|[\\s"'\`:])(?:${UTILITIES})-(?:${PALETTE})(?:-\\d{2,3})?(?:/\\d+)?(?![\\w-])`),
    'Tailwind palette colour: use a token utility',
  ],
  [/(?:^|[\s"'`])dark:/, 'dark: variant: tokens switch themes, so components need none'],
  // bg-primary/50 compiles to color-mix(), which needs Chrome 111 / Safari 16.4: make a token.
  [/\b(?:bg|text|border|outline|ring|fill|stroke|divide|placeholder|decoration|shadow)-[a-z-]+\/\d+\b/, 'opacity modifier (color-mix, newer engines only): add a token'],
];
function walk(dir: string): string[] {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return e.name === 'node_modules' ? [] : walk(path);
    return /\.(tsx?|css|html)$/.test(e.name) ? [path] : [];
  });
}
const scanned = [
  ...walk(join(design, 'src')),
  ...walk(previewsDir),
  ...walk(join(design, 'scripts')),
  ...walk(join(web, 'src')),
  join(web, 'index.html'),
].filter((f) => !f.endsWith('tokens.css') && !f.endsWith('.gen.ts') && !f.endsWith('check.ts'));
for (const file of scanned) {
  readFileSync(file, 'utf8')
    .split('\n')
    .forEach((line, i) => {
      // Comments may name colours; code may not.
      if (/^\s*(\/\/|\/?\*)/.test(line)) return;
      for (const [re, why] of rules) {
        const hit = re.exec(line);
        if (hit) problems.push(`${relative(web, file)}:${String(i + 1)}: ${why} (${hit[0].trim()})`);
      }
    });
}

// ---- Built pages carry their markers ----------------------------------------------------------

if (!existsSync(join(outDir, 'styles.css'))) problems.push('dist/styles.css missing: run pnpm --dir web build:design');
for (const { name, group } of previews) {
  const page = join(outDir, 'previews', `${name}.html`);
  if (!existsSync(page)) {
    problems.push(`dist/previews/${name}.html missing: run pnpm --dir web build:design`);
    continue;
  }
  const first = readFileSync(page, 'utf8').split('\n', 1)[0];
  if (first !== `<!-- @dsCard group="${group}" -->`) problems.push(`dist/previews/${name}.html: first line is ${first}`);
}

if (problems.length) {
  console.error(problems.join('\n'));
  console.error(`check:design: ${String(problems.length)} problem(s)`);
  process.exit(1);
}

// ---- Accessibility in Chromium, light and dark ------------------------------------------------

const server = await preview({
  configFile: false,
  root: design,
  logLevel: 'warn',
  build: { outDir },
  preview: { host: '127.0.0.1', port: 0, strictPort: false },
});
const origin = server.resolvedUrls?.local[0];
if (!origin) throw new Error('vite preview did not start');

const browser = await chromium.launch();
let failures = 0;
try {
  for (const colorScheme of ['light', 'dark'] as const) {
    const context = await browser.newContext({ colorScheme });
    for (const name of ['index', ...previews.map((p) => p.name)]) {
      const page = await context.newPage();
      const errors: string[] = [];
      page.on('pageerror', (e) => errors.push(e.message));
      page.on('console', (m) => {
        if (m.type() === 'error') errors.push(m.text());
      });
      await page.goto(`${origin}previews/${name}.html`);
      if (name !== 'index') await page.locator('[data-story]').first().waitFor({ timeout: 10_000 });
      const results = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'])
        .analyze();
      for (const v of results.violations) {
        const blocking = v.impact === 'serious' || v.impact === 'critical';
        if (blocking) failures++;
        const targets = v.nodes.map((n) => n.target.join(' ')).join(', ');
        console.log(`${blocking ? 'FAIL' : 'note'} ${name} (${colorScheme}): ${String(v.impact)} ${v.id}: ${targets}`);
      }
      for (const e of errors) {
        failures++;
        console.log(`FAIL ${name} (${colorScheme}): ${e}`);
      }
      await page.close();
    }
    await context.close();
  }
} finally {
  await browser.close();
  await new Promise<void>((resolve, reject) => {
    server.httpServer.close((e) => {
      if (e) reject(e);
      else resolve();
    });
  });
}

const pages = previews.length + 1;
console.log(`check:design: ${String(pages)} pages in light and dark, ${String(failures)} blocking problem(s)`);
if (failures) process.exit(1);
