# Development and deployment

## Local environment

Use the pinned versions in `go.mod`, `.node-version` and `web/package.json`. Put the matching
Node and pnpm on `PATH` before running Make; an older pnpm may not understand the lockfile. Go
downloads the requested toolchain when necessary. Docker (or Colima on macOS) supplies a
disposable PostgreSQL; set `PG_PORT` if 54329 is taken.

```sh
make setup
make dev
```

`make dev` creates `.env` from `.env.example`. Without integrations the app runs and the search
page says what's missing. To try the whole flow without real services, `make e2e` runs the
binary against `web/e2e/fake-services.ts`, a fake Prowlarr and qBittorrent.

## Configuration

Environment only. Secrets have no defaults.

| Variable | Default | |
| --- | --- | --- |
| `DATABASE_URL` | (required) | PostgreSQL; TLS unless localhost |
| `LISTEN_ADDR` | `:8080` | |
| `ALLOW_SIGNUP` | `false` | Turn on once to create accounts |
| `SESSION_TTL` | `720h` | |
| `AUTH_RATE_PER_MINUTE` | `10` | Sign-in and sign-up attempts per client |
| `PASSWORD_LOGIN` | `true` | Email and password sign-in; turn off to sign in only through OIDC |
| `OIDC_ISSUER` | | OIDC provider (e.g. Pocket ID), https; turns OIDC sign-in on |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | | The client registered at the provider; the secret is empty for a public client |
| `OIDC_REDIRECT_URL` | | `https://<host>/api/auth/oidc/callback`, registered at the provider |
| `OIDC_ALLOWED_GROUPS` | | Comma-separated: only members of one may sign in (asks for the `groups` scope) |
| `OIDC_LIMITED_GROUPS` | | Comma-separated: members sign in as limited users (children). They browse and request only catalog games no rating board rates mature (ESRB M/AO, PEGI 18, ...) and can't search releases. A member of an allowed group too is not limited |
| `OIDC_GROUPS_CLAIM` | `groups` | The ID token claim listing groups |
| `OIDC_NAME` | `single sign-on` | The provider's name on the sign-in button |
| `TRUSTED_PROXIES` | | CIDRs whose `X-Forwarded-For` is believed |
| `LOG_LEVEL` | `info` | |
| `PROWLARR_URL`, `PROWLARR_API_KEY` | | Enables search |
| `PROWLARR_CATEGORIES` | all | Comma-separated Newznab ids, e.g. `1000` (Console) |
| `QBITTORRENT_URL` | | Enables downloads (with the library and both paths below) |
| `QBITTORRENT_USERNAME`, `QBITTORRENT_PASSWORD` | | Omit both if qBittorrent bypasses auth for this client |
| `IGDB_CLIENT_ID`, `IGDB_CLIENT_SECRET` | | The game catalog: a Twitch application's credentials (the ones RomM uses). Needs `LIBRARY_DIR` |
| `CATALOG_PLATFORMS` | | `folder=igdb-slug` pairs for library folders IGDB knows by another name, e.g. `md=genesis-slash-megadrive` |
| `QBITTORRENT_CATEGORY` | | An existing category for ROM Request's torrents |
| `QBITTORRENT_SAVE_PATH` | | Absolute, as qBittorrent sees it; each request downloads to `<this>/<request id>` |
| `DOWNLOAD_DIR` | | The same folder as ROM Request sees it; read-only is enough |
| `LIBRARY_DIR` | | The ROM library, writable; enables the platform list |
| `LIBRARY_EXCLUDE` | `bios` | Top-level folders that aren't platforms |
| `WORKER_INTERVAL` | `15s` | How often downloads are checked (1s to 1h) |

Keep `QBITTORRENT_SAVE_PATH` and `DOWNLOAD_DIR` pointing at the same storage, and preferably a
folder of its own. Disable qBittorrent's "remove torrent when ratio reached" for the
`rom-request` tag or category until imports finish, or the request fails with
`torrent_removed`.

## Checks

| Command | Purpose |
| --- | --- |
| `make gen` | Regenerate the Go API, sqlc queries and TypeScript client |
| `make lint` | Go, TypeScript, ESLint and the OpenAPI contract |
| `make test` | Go tests against disposable PostgreSQL, web unit tests |
| `make e2e` | The real binary in Chromium, against fake Prowlarr and qBittorrent |
| `make design` | Component previews, tokens and accessibility |
| `make security` | Dependency vulnerability scans |
| `make chart` | Lint and validate the Helm chart |
| `make ci` | Everything CI runs, plus the container build |

Follow [AGENTS.md](../AGENTS.md) when adding endpoints.

## Deploying

`main` builds a multi-architecture image, `ghcr.io/oldsj/rom-request`, and publishes new chart
versions to `oci://ghcr.io/oldsj/charts/rom-request`. Pin both by version and digest in your
own values file; [`ci/full.yaml`](../charts/rom-request/ci/full.yaml) shows every integration:

- `secrets.existingSecret` with `PROWLARR_API_KEY` and the qBittorrent credentials;
- `integrations.*` for the URLs, category and save path;
- `storage.downloads` (mounted read-only) and `storage.library` (read-write) as volume sources,
  NFS or ReadWriteMany claims, since replicas share them;
- `networkPolicy.extraEgress` for Prowlarr and qBittorrent, because the default policy only
  allows DNS and the database.

The pod runs as uid 65532; the library must be writable by it (for NFS, a squash or an export
rule for the cluster's nodes). RomM's own mount of the library can stay read-only.
