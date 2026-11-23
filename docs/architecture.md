# Architecture

## Components

| Component | Responsibility |
| --- | --- |
| React frontend | Search releases, choose a platform, record license and source, follow requests |
| Go API | Sessions, search, requests; the only thing that talks to the integrations |
| Request worker | Runs in the same binary: submits torrents, follows downloads, imports |
| PostgreSQL | Users, sessions, short-lived search results, requests and their progress |
| Prowlarr | Searches the operator's indexers |
| qBittorrent | Downloads each request into its own folder |
| ROM library | The `<platform>/` folders RomM scans; ROM Request adds files to it |
| RomM | Scans the library and plays the games |

Integration credentials stay in server configuration. A Prowlarr result's download link carries
Prowlarr's API key, so search results are stored server-side for an hour and the browser only
sees their ids.

## Request lifecycle

```text
queued ──▶ downloading ──▶ importing ──▶ imported
   │            │              │
   └────────────┴──────────────┴──▶ failed ──(retry)──▶ queued
```

1. **Search.** `GET /api/search` asks Prowlarr, keeps torrent results, stores them for the user,
   and returns them by seeders.
2. **Request.** `POST /api/requests` takes a result id and a platform (a library folder). The
   API reads the release (a `.torrent` file or magnet link, following Prowlarr's redirects) to
   get its info-hash. One live request per torrent.
3. **Submit.** The torrent is added to qBittorrent with save path
   `QBITTORRENT_SAVE_PATH/<request id>`, automatic torrent management off and the
   `rom-request` tag. The request becomes `downloading`. If qBittorrent is unreachable, the
   request stays `queued` and the worker submits it later; submitting is idempotent.
4. **Follow.** Every `WORKER_INTERVAL` the worker reads the active torrents by hash and records
   progress. A torrent that disappears or errors fails the request.
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
6. **Done.** The request records the path, file count and (for one file) SHA-256. RomM picks it
   up at its next scan.

Each step is an `UPDATE ... WHERE status = ...`, so two replicas never take the same step. An
import interrupted by a crash is reclaimed after two hours. Removing a request that is still
downloading deletes its torrent and downloaded files; an imported game stays in the library.

## Failures

| Code | Meaning | Next step |
| --- | --- | --- |
| `library_conflict` | A file or folder with that name exists | Rename or remove it in the library, retry |
| `unknown_platform` | The platform folder is gone | Recreate it, retry |
| `download_incomplete` | Files missing or the wrong size | Recheck the torrent in qBittorrent, retry |
| `unsafe_path` | A path in the torrent escapes its folder | Don't import this release |
| `torrent_removed`, `download_failed` | qBittorrent lost or errored the torrent | Retry adds it again |
| `already_in_qbittorrent` | The torrent exists outside ROM Request's folder | Remove it there, retry |
| `release_unreadable`, `release_changed`, `qbittorrent_rejected` | The release itself is bad | Pick another release |

## Library layout and RomM

ROM Request writes RomM's
[default structure](https://docs.romm.app/latest/Getting-Started/Folder-Structure/)
(`<library>/<platform>/<game>`) and treats top-level folders as platforms, except
`LIBRARY_EXCLUDE` (default `bios`) and names starting with `.`, `#` or `@`. RomM only reads the
library; ROM Request needs its own writable mount of the same folder. Add `.rom-request` to
`exclude.platforms` in RomM's `config.yml`, and let RomM's scheduled rescan find new games
(RomM's API is behind its own sign-in, so ROM Request doesn't trigger scans).

## Scope

One Prowlarr, one qBittorrent and one library. Not included: Usenet, extracting archives,
mirroring collections on a schedule, syncing to handheld devices, and verifying licenses.
