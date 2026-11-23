# AGENTS.md

ROM Request: one Go binary serves a JSON API under `/api`, the embedded React SPA, and a
background worker that follows requests from Prowlarr search through qBittorrent into the ROM
library RomM scans.
`CLAUDE.md` only imports this file; edit this one.

## Layout

```text
api/openapi.yaml        The wire contract. Go server types and the TS client are generated from it.
cmd/app/                main: `app serve` (default) and `app migrate`
internal/config/        The only package that reads the environment (lint-enforced)
internal/db/            Pool, goose migrations (migrations/*.sql), sqlc queries (queries/*.sql)
internal/db/dbq/        sqlc output. Generated: never edit.
internal/api/           oapi-codegen output (strict server interface + models). Generated: never edit.
internal/server/        Handlers (implement api.StrictServerInterface), routing, sessions
internal/httpx/         Middleware: security headers, client IP, rate limit, body limit, logs, recover
internal/auth/          argon2id passwords, session tokens
internal/prowlarr/      Prowlarr search client
internal/qbittorrent/   qBittorrent Web API client
internal/torrent/       Torrent ids from .torrent files and magnet links
internal/library/       Platform folders and the import (os.Root-confined, never overwrites)
internal/requests/      Request lifecycle: submit, follow downloads, import (the worker)
internal/testdb/        A fresh migrated Postgres database per integration test
web/                    React + Vite app; web/src/api/schema.gen.ts is generated
web/design/             The design system (@app/design): tokens, base components, previews (its README)
web/*.go                Go package that embeds web/dist (build tag embedweb) and serves it
charts/<app>/           Helm chart, published to oci://ghcr.io/<owner>/charts
tools/go.mod            Pinned Go tools (oapi-codegen, sqlc, golangci-lint, govulncheck, air)
```

## Rules

- **Contract first.** Change `api/openapi.yaml`, then `make gen`. Never edit `*.gen.go`,
  `internal/db/dbq/` or `web/src/api/schema.gen.ts`.
- **SQL only in `internal/db/queries/*.sql`**, through sqlc. Never build SQL from strings. Every
  query on user data takes the owner's id (`WHERE ... AND user_id = $n`).
- **Schema changes are new goose migrations** (`internal/db/migrations/NNNNN_name.sql`); never edit
  one that has been merged.
- **The web app calls the API only through `web/src/api/client.ts`** (`$api` hooks, or `client`).
  ESLint forbids `fetch` and `dangerouslySetInnerHTML`.
- **Errors** are `{"error": {"code", "message"}}`: `code` is stable snake_case, `message` is safe
  to show. Return typed responses from handlers; return a Go `error` only for unexpected failures
  (logged, and the client gets a generic 500).
- **UI comes from the design system.** Build screens from `@app/design` components and token
  utilities (`bg-surface`, `text-muted-foreground`); never a hex colour, a Tailwind palette class
  (`stone-500`) or `dark:` (`make design` fails on them). Something new goes into `web/design`
  first, with a preview: `web/design/README.md`.
- **No inline `<script>`, `<style>` or `style=` in the web app.** The CSP forbids them and the e2e
  test fails on any CSP violation. Use Tailwind classes.
- **Config** is env only, read in `internal/config`. Secrets have no defaults. Add new variables
  to `.env.example` and to the chart.
- **Integrations stay on the server.** Prowlarr download links carry its API key: never return
  them (or qBittorrent details) from the API. File access goes through `internal/library`, which
  reads only below `DOWNLOAD_DIR`, writes only below `LIBRARY_DIR`, and never replaces a file.
- **Worker steps are conditional updates** (`WHERE status = ...`), so two replicas can run the
  worker at once. Keep new steps that way.
- Don't weaken security headers, CSP, cookie flags, rate limits or tests to make something pass.

## Add an endpoint, end to end

Example: let users pin a request to the top of their list.

1. **Contract.** In `api/openapi.yaml` add the path and operation (`operationId: pinRequest`),
   request and response schemas with limits (`maxLength`, `minimum`, `additionalProperties:
   false`), and `$ref` the shared error responses. Leave `security` at the default (signed in)
   unless it's public, then set `security: []`.
2. **Schema.** `internal/db/migrations/00003_pinned.sql`:
   `-- +goose Up` / `ALTER TABLE requests ADD COLUMN pinned boolean NOT NULL DEFAULT false;` /
   `-- +goose Down` / `ALTER TABLE requests DROP COLUMN pinned;`
3. **Query.** In `internal/db/queries/requests.sql`:
   `-- name: SetRequestPinned :one` / `UPDATE requests SET pinned = $3 WHERE id = $1 AND user_id = $2 RETURNING *;`
4. **Generate.** `make gen`. The build now fails until `*Server` implements `PinRequest`, and the
   TS types know the new operation.
5. **Handler.** Add `PinRequest` in `internal/server/requests.go`: take the user with `userFrom(ctx)`,
   call `s.q.SetRequestPinned`, map `pgx.ErrNoRows` to the typed 404, return the typed 200.
   Request validation (types, lengths, formats) already happened against the spec.
6. **Test.** Extend `internal/server/requests_test.go` (real Postgres, fake Prowlarr and
   qBittorrent from `fakes_test.go`): happy path, 404 for another user's request, 401 signed out.
7. **UI.** Call `$api.useMutation('post', '/requests/{requestId}/pin', { onSuccess: invalidate })`
   from `web/src/routes/requests.tsx`, with a `Button` from `@app/design`. Add a Vitest test for
   non-trivial UI logic; extend `web/e2e/app.spec.ts` (which runs against
   `web/e2e/fake-services.ts`) only for a core flow.
8. **Check.** `make lint test`, then `make e2e`. Before a PR, `make ci`.

## Commands

```bash
make setup     # pnpm install + Playwright browser
make dev       # Postgres + Go API with hot reload (:8080) + Vite (http://localhost:5173)
make gen       # regenerate from api/openapi.yaml and internal/db/
make fmt       # format Go, eslint --fix
make lint      # golangci-lint (incl. gosec), tsc, eslint, redocly
make design    # design system: build previews, check tokens, @dsCard markers, axe (light + dark)
make test      # go test -race against Postgres in Docker, vitest
make e2e       # build the real binary and run Playwright against it
make security  # govulncheck, pnpm audit (+ osv-scanner if installed)
make chart     # helm lint, kubeconform -strict, chart version-bump check
make ci        # all of the above, plus the multi-arch image build
```

Integration tests need `TEST_DATABASE_URL`; `make test` sets it to the Compose Postgres. Without
it they skip locally and fail in CI.

## Deploying

The chart in `charts/<app>/` is the deploy artifact. Any change under it needs a `version` bump
in `Chart.yaml` (`make chart` checks). A release sets `appVersion` to the image tag (the commit
SHA) and `image.digest` in the deployment's own values file, kept in a separate GitOps
repository. Don't deploy from this repository.
