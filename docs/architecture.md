# Architecture

## Components

| Component | Responsibility |
| --- | --- |
| React frontend | Search releases, choose a platform, record license and source, follow requests |
| Go API | Sessions, search, requests; the only thing that talks to the integrations |
| Request worker | Runs in the same binary: submits torrents, follows downloads, imports |
| IGDB | The catalog behind Browse: games per platform and their covers. The server calls it (covers too, so the CSP stays same-origin) with a Twitch app token it caches; "in library" compares normalized names with the platform folder's files |
| PostgreSQL | Users, sessions, OIDC sign-ins in progress, short-lived search results, requests and their progress |
| Prowlarr | Searches the operator's indexers |
| qBittorrent | Downloads each request into its own folder |
| ROM library | The `<platform>/` folders RomM scans; ROM Request adds files to it |
| RomM | Scans the library and plays the games |

Integration credentials stay in server configuration. A Prowlarr result's download link carries
Prowlarr's API key, so search results are stored server-side for an hour and the browser only
sees their ids.

## Request lifecycle

```text
searching ──▶ queued ──▶ downloading ──▶ importing ──▶ imported
    │             │             │              │
    └─────────────┴─────────────┴──────────────┴──▶ failed ──(retry)──▶ queued (or searching)
```

A request from Browse (`POST /api/catalog/requests`, a game id and a platform folder) starts as
`searching`, with no release yet. The worker claims it (`ClaimSearch`, so one pod searches it),
searches Prowlarr for the game's name and then its significant words, ranks the results
(`internal/requests/pick.go`), reads the best few torrents and moves the request to `queued`
with the first one no other request has. A search can take minutes with slow indexers, so it
runs in the worker, not in the browser's request. No match fails the request with `no_release`;
Prowlarr errors are retried every ten minutes, four times. Retrying a catalog request without a
release searches again. One live catalog request per user, platform and game.

Manual requests, from Search:

1. **Search.** `GET /api/search` asks Prowlarr, keeps torrent results, stores them for the user,
   and returns them by seeders.
2. **Request.** `POST /api/requests` takes a result id and a platform (a library folder). The
   API reads the release through Prowlarr's download proxy (a `.torrent` file, or a redirect to
   a magnet link) to get its info-hashes. One live request per torrent.
3. **Submit.** The torrent is added to qBittorrent with save path
   `QBITTORRENT_SAVE_PATH/<request id>`, automatic torrent management off and the
   `rom-request` tag. The request becomes `downloading`. If qBittorrent or the indexer is
   unreachable, the request stays `queued` and the worker tries again with a growing backoff
   (1 minute to an hour), giving up after ten attempts. Submitting is idempotent: the worker
   looks for the torrent in qBittorrent before fetching the release again.
4. **Follow.** Every `WORKER_INTERVAL` the worker reads the active torrents and records
   progress. A torrent can have two ids (a hybrid v1 + v2 torrent is its v1 hash on libtorrent
   1.2 builds and its truncated v2 hash on 2.x); the request keeps both. A torrent that errors,
   or is still missing two minutes after it was added, fails the request.
5. **Import.** When qBittorrent reports the torrent complete and seeding, the worker claims the
   request (`downloading → importing`) and imports the wanted files (it skips files set to "do
   not download") from `DOWNLOAD_DIR/<request id>`:
   - one file lands as `<platform>/<file>`;
   - several files land as `<platform>/<folder>`, the torrent's own top folder or one named
     after the release, which RomM reads as one multi-file game, with any license and readme
     files beside it.

   Files are copied into `LIBRARY_DIR/.rom-request/<request id>`, synced, checked against the
   sizes qBittorrent reported, hashed, and only then moved into place with an operation that
   fails rather than replace anything. The download stays in qBittorrent, seeding.
   Web pages, images and metadata files (`.html`, `.json`, `.jpg`, `.png` and the like) are
   never imported. A download with no game file for its platform (the platform's ROM types, or
   a `.zip`/`.7z`, which RomM reads for any platform) fails with `no_rom` and nothing reaches
   the library: some indexers serve their own landing page as the "release". A catalog request
   then forgets that release and removes its torrent, so Retry searches for another.
6. **Done.** The request records the path, file count and (for one file) SHA-256. With
   `ROMM_URL` and `ROMM_API_TOKEN` set, the worker asks RomM for a quick scan of that platform
   folder (`POST /api/tasks/scan`); if RomM is busy or down it asks again each round. Without
   them, RomM finds the game at its next scheduled scan.

Each step is an `UPDATE ... WHERE status = ...`, so two replicas never take the same step. An
import holds a claim it renews every 30 seconds; only the holder can finish it. On shutdown an
import stops and hands its claim back, so the next round starts it again; one whose pod died is
taken over after five minutes without renewal, and an importer that can't renew stops before
then. Removing a request that isn't imported deletes its torrent and downloaded files, at once
or, while qBittorrent is down, from a queue the worker works through; an imported game stays in
the library.

## Failures

| Code | Meaning | Next step |
| --- | --- | --- |
| `no_rom` | The download held no game file for the platform (a web page, images) | Catalog: Retry searches for another release. Manual: pick another result |
| `library_conflict` | A file or folder with that name exists | Rename or remove it in the library, retry |
| `unknown_platform` | The platform folder is gone | Recreate it, retry |
| `download_incomplete` | Files missing or the wrong size | Recheck the torrent in qBittorrent, retry |
| `unsafe_path` | A path in the torrent escapes its folder | Don't import this release |
| `torrent_removed`, `download_failed` | qBittorrent lost or errored the torrent | Retry adds it again |
| `submit_failed` | qBittorrent or the indexer was unreachable for hours | Retry once they're back |
| `already_in_qbittorrent` | The torrent exists outside ROM Request's folder | Remove it there, retry |
| `release_unreadable`, `release_changed`, `qbittorrent_rejected` | The release itself is bad | Pick another release |

## Library layout and RomM

ROM Request writes RomM's
[default structure](https://docs.romm.app/latest/Getting-Started/Folder-Structure/)
(`<library>/<platform>/<game>`) and treats top-level folders as platforms, except
`LIBRARY_EXCLUDE` (default `bios`) and names starting with `.`, `#` or `@`. RomM only reads the
library; ROM Request needs its own writable mount of the same folder. Add `.rom-request` to
`exclude.platforms` in RomM's `config.yml`.

New games reach RomM two ways. RomM's own scheduled rescan (`ENABLE_SCHEDULED_RESCAN`) is the
backstop. For a game to show up within a minute, give ROM Request a RomM client API token
(RomM 5.4+: an admin's Profile → Client API tokens, scope `tasks.run` only) as
`ROMM_API_TOKEN`, with `ROMM_URL`: after each import it asks for a quick scan (new files only)
of that platform folder. The token is a bearer token for RomM's API only; RomM's sign-in
(OIDC, say) is unchanged. RomM's filesystem watcher doesn't see writes made over NFS from
another machine, so don't rely on it.

`PROWLARR_BLOCKED_INDEXERS` (default `BlueRoms`) drops an indexer's results from every search;
BlueRoms answers with its own web page and images, not the game.

## Scope

One Prowlarr, one qBittorrent and one library. Not included: Usenet, extracting archives,
mirroring collections on a schedule, syncing to handheld devices, and verifying licenses.
