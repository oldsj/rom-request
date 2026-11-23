# ROM Request

A self-hosted home for discovering and collecting openly licensed retro games.

ROM Request makes Creative Commons releases, public-domain games and creator-distributed homebrew
easier to find and bring into a personal [RomM](https://romm.app/) library. Search your indexers,
pick a platform, and follow each request from download to library.

```text
ROM Request → Prowlarr → qBittorrent → ROM library → RomM
 request       search     download      import       play
```

## What it does

- **Browse** every game IGDB lists for each platform in your library (the home page), marked when
  you have it, and **request** one with a click: the worker searches Prowlarr for the game, picks
  a release (every word of the title, the right platform, no soundtracks, cracks or packs;
  preferring the USA, then Europe, then more seeders), and downloads and imports it like any
  other request.
- **Search** the indexers configured in your [Prowlarr](https://prowlarr.com/), with torrent results
  sorted by seeders.
- **Request** a release for a platform. The platforms are your library's top-level folders (`GBA`,
  `PS1`, ...), the layout RomM scans. You can record the release's license and its release page;
  both stay with the request and show as unknown when you leave them empty.
- **Download** through your existing [qBittorrent](https://www.qbittorrent.org/). Each request
  gets its own folder, and its torrent is identified by its info-hash, never by a similar name.
- **Import** into the library once the download finishes. A single file lands as
  `<platform>/<file>`. Several files stay together as `<platform>/<folder>`, which RomM reads as one
  multi-file game; any license and readme files stay beside it. Nothing in the library is ever
  overwritten: a name that already exists is reported as a conflict for you to settle, and the
  request can then be retried. The downloaded release itself is left as it was.
- **Follow** each request through queued, downloading (with progress), importing, and in library or
  failed. A failure says what to do next. Progress survives restarts, and two replicas can run
  side by side.

RomM shows new games after its next library scan; schedule one in RomM (for example its
scheduled rescan).

## Built around open game collections

The intended use is collecting games whose creators or rightsholders permit the relevant
distribution and use:

- Games released under a Creative Commons license, with the applicable terms preserved.
- Games dedicated to the public domain or accompanied by a documented public-domain status.
- Homebrew and independent games shared by their creators with redistribution permission.

Homebrew describes how a game was made, not its license, and a free download doesn't by itself
grant permission to redistribute it. Use the publisher's release page and license as the source
of truth.

Prowlarr returns listings from the indexers you configure; it doesn't establish a release's
license, and neither does ROM Request: it records what the requester enters and shows it, without
verifying it or filtering results. See [content sources and attribution](docs/content-sources.md).

## Run it

ROM Request is one Go binary (or container image) with PostgreSQL. Point it at your services with
environment variables:

| Variable | What |
| --- | --- |
| `DATABASE_URL` | PostgreSQL (TLS required except on localhost) |
| `PROWLARR_URL`, `PROWLARR_API_KEY` | Search. `PROWLARR_CATEGORIES` limits it (`1000` is Console) |
| `QBITTORRENT_URL`, `QBITTORRENT_USERNAME`, `QBITTORRENT_PASSWORD` | Downloads |
| `QBITTORRENT_SAVE_PATH` | The folder, as qBittorrent sees it, that requests download into |
| `DOWNLOAD_DIR` | The same folder as ROM Request sees it (read-only is enough) |
| `LIBRARY_DIR` | The ROM library, writable; `LIBRARY_EXCLUDE` (default `bios`) lists folders that aren't platforms |
| `IGDB_CLIENT_ID`, `IGDB_CLIENT_SECRET` | Browse: every game IGDB lists for each library platform, marked when you have it (the Twitch app RomM uses) |

Every integration is optional; the app says what isn't set up. Add `.rom-request` (the import
staging folder) to RomM's `exclude.platforms`.

To sign in, either use an OIDC provider such as [Pocket ID](https://pocket-id.org) (set
`OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` and `OIDC_REDIRECT_URL`, optionally
`OIDC_ALLOWED_GROUPS`, and `PASSWORD_LOGIN=false` once it works) or start once with
`ALLOW_SIGNUP=true` to create a password account. All settings are in
[development and deployment](docs/development.md).

For Kubernetes, the Helm chart is in [`charts/rom-request`](charts/rom-request) and is published
to `oci://ghcr.io/oldsj/charts/rom-request`; [`ci/full.yaml`](charts/rom-request/ci/full.yaml)
is an example with every integration.

## Develop

Use the versions pinned by `go.mod`, `.node-version` and `web/package.json`, with Docker for
PostgreSQL.

```sh
make setup
make dev       # API on :8080 with hot reload, Vite on http://localhost:5173
make lint test
make e2e       # the real binary against fake Prowlarr and qBittorrent
make ci        # everything CI runs
```

See [architecture](docs/architecture.md), [development and deployment](docs/development.md),
[security](SECURITY.md) and [repository conventions](AGENTS.md).

## License

[Apache License 2.0](LICENSE).
