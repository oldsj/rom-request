## Using @app/design

No provider or wrapper is needed: import the components and render them. `styles.css` (loaded by
the design tool) holds every token and every class the components use.

### Components

`Button`, `Input`, `Textarea`, `Select`, `Panel`, `Notice`, `Badge` and `Progress`. Build screens
from these rather than hand-rolling lookalikes; if a screen needs something new, it becomes a component here first.

- `Button` takes `variant="primary" | "secondary" | "danger"` (default `secondary`) and
  `fullWidth`. One `primary` per view. `danger` deletes or ends something. `type` defaults to
  `"button"`.
- `Input` and `Textarea` take `label` (required and visible; placeholders are not labels), `hint`
  and `error` (marks the control invalid and is read with it), plus the native element's props.
  `className` styles the wrapper.
- `Select` is `Input`'s native `<select>` counterpart: the same `label`, `hint` and `error`, with
  `<option>` children.
- `Panel` takes `as="section" | "article" | "div" | "li"`, optional `heading` (an `h2`) and
  `description`. Don't nest panels.
- `Notice` takes `tone="info" | "error"`. Errors are announced at once (`role="alert"`), info
  politely (`role="status"`).
- `Badge` is a short status label: `tone="neutral" | "active" | "success" | "danger"`. The text
  carries the meaning; colour only repeats it.
- `Progress` is a labelled bar: `label` and `value` from 0 to 1. It is a native `<progress>`, so it
  needs no inline style.

Every component's `className` extends its classes; it never replaces them.

### Tokens

Layout around the components uses the token utilities (`bg-background`, `bg-surface`,
`bg-surface-muted`, `text-foreground`, `text-muted-foreground`, `border-border`, `border-input`,
`text-danger`, `text-success`, `outline-ring`, `rounded-control`, `rounded-panel`) or, in plain CSS, the variables
(`var(--surface)`). There is no Tailwind palette (`stone-100` and the like generate nothing) and no
`dark:` variant: the tokens switch with the theme. Never write a hex or `rgb()` colour.

Light is the default; dark follows the system, and `<html data-theme="light|dark">` pins one.

### Accessibility

Targets are at least 44px tall (`min-h-11`). Focus is a visible 2px `outline-ring` outline. Text
meets 4.5:1 and control borders 3:1 in both themes.
