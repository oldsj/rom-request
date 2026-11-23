# Design system

This app's own look: tokens, base components and a preview of each, as a pnpm workspace package
(`@app/design`) that the web app imports. Each app made from the template grows its own; only the
shape is shared. The same files sync to a Claude Design design-system project, so designs made
there use the components and tokens the app ships.

```text
src/tokens.css            Colour tokens as CSS variables, light and dark. The only place a colour is defined.
src/theme.css             Tailwind 4 theme: maps tokens to utilities (bg-surface, ...), radius, font.
src/styles.css            What the app imports after Tailwind: tokens + theme + the components' sources.
src/components/*.tsx      The components. Exported from src/index.ts.
.design-sync/previews/    One <Component>.tsx per component: each named export is a story.
.design-sync/config.json  design-sync settings (package shape, CSS entry, build command).
.design-sync/conventions.md  How to use the components, shown to Claude Design. Keep it current.
scripts/previews.ts       Builds every preview into dist/previews/<Component>.html, and dist/styles.css.
scripts/check.ts          The checks (below).
```

The app uses it like this (`web/src/index.css`, then any route):

```tsx
// index.css: @import 'tailwindcss'; @import '@app/design/styles.css';
import { Button, Input, Panel } from '@app/design';
```

## Commands

From the repository root:

```bash
make design                      # build the previews, then run every check (CI runs this)
pnpm --dir web build:design      # just build: open web/design/dist/previews/index.html via a server
pnpm --dir web check:design      # just check (after a build)
```

`check:design` fails when:

- a component exported from `src/index.ts` has no `.design-sync/previews/<Name>.tsx`;
- a preview's first line isn't `// @dsCard group="<Group>"`, or its built page's first line isn't
  the matching `<!-- @dsCard group="<Group>" -->` (Claude Design groups its cards by it);
- a theme in `tokens.css` lacks a token the default defines, or adds one;
- the repository's `DESIGN.md`, if it has one, lists a colour in its YAML front matter that
  differs from `tokens.css`;
- a component, preview or app file (`web/src`, `web/index.html`) has a raw colour, a Tailwind
  palette class, a `dark:` variant or an opacity modifier (`bg-primary/50`);
- axe finds a serious or critical WCAG 2.2 A/AA problem on any preview page, in light or dark, or a
  page throws.

## Browsers

Tailwind 4 puts every utility in a cascade layer, so the floor is Chrome (and Android WebView) 99,
Safari 15.4 and Firefox 97: an older engine ignores the layered rules and shows unstyled controls.
Opacity modifiers would raise it to Chrome 111 and Safari 16.4 (`color-mix()`), so the check
forbids them: add a token instead. Tokens are plain hex, not `oklch()`, for the same reason.

## Grow it

- **New token**: add it to `:root` and to both dark blocks in `tokens.css`, map it in `theme.css`
  (`--color-<name>: var(--<name>)`), and name it in `.design-sync/conventions.md`. Check the
  contrast against the surfaces it sits on.
- **New component**: `src/components/<Name>.tsx` using token utilities only, export it from
  `src/index.ts`, add `.design-sync/previews/<Name>.tsx` (first line `// @dsCard group="<Group>"`,
  one named export per state worth seeing: default, disabled, error, empty, long text), describe
  its props in `conventions.md`, then `make design`. Add a Vitest test
  (`src/components/<Name>.test.tsx`) when it has logic, such as ARIA wiring.
- Screens stay in the app (`web/src/routes`). A pattern used on two screens becomes a component
  here.
- Fictional data only in previews: they are uploaded to Claude Design.

## Sync with Claude Design

Pushing to claude.ai/design is something you start; agents don't. In Claude Code, from this
folder:

1. `make design` from the repository root, and fix anything it reports.
2. `cd web/design`, start Claude Code, and run `/design-sync`. It reads
   `.design-sync/config.json`, builds with `buildCmd`, and renders the previews into cards.
3. The first time, pick **Create new** when it lists design-system projects (name it after the
   app). It records the project's id in `.design-sync/config.json`; commit that.
4. Review the plan it shows (the files it will write or delete), approve it, and let it sync. It
   goes one component at a time and never replaces the whole project.
5. Commit what it changed under `.design-sync/` (it may add `NOTES.md` with what it learned about
   rendering this package). Its caches (`.ds-sync/`, `ds-bundle/`, `.design-sync/.cache/`) are
   git-ignored.

Re-run `/design-sync` after changing a component, its preview or the tokens. A change only to a
component's styling keeps the card's previous review grade, so look at its new card before you
trust it.
