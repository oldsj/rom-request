# Content sources and attribution

ROM Request's intended collection workflow centers on Creative Commons releases, public-domain
games, and homebrew distributed with permission. Each request records the license and release
page the requester enters; ROM Request does not check them.

## Choose sources with a clear release history

Start with a creator's or publisher's release page. Look for an explicit license or distribution
statement covering the game files you intend to collect. An indexer listing is a discovery aid;
keep the original release page as the provenance reference.

Useful collection types include a creator's CC-licensed releases, a documented public-domain
collection, or a homebrew collection whose authors permit redistribution. ROM Request ships
with no preselected indexers or game sources.

Read the terms for the particular release. Creative Commons offers several licenses with
different conditions, and CC0 is a separate public-domain dedication tool. The
[Creative Commons license overview](https://creativecommons.org/cc-licenses/) and
[public-domain tools](https://creativecommons.org/public-domain/) explain those distinctions.

## Keep a source record

ROM Request keeps the title, indexer, release page, license and file checksum with each request.
A fuller record retains these details when available:

| Field | What to record |
| --- | --- |
| Title and version | The release being imported, not just the franchise or series |
| Creator | The author or rightsholder named by the release |
| Original release URL | A creator or publisher page identifying the release |
| Distribution URL | Where the selected file or torrent is offered |
| License | The exact identifier, version, or permission statement supplied |
| License URL | The license text or the publisher's distribution terms |
| Attribution | The creator's supplied credits and notices |
| File identity | Filename, size, and checksum when available |
| Review date | When the source and permission statement were checked |

Missing or ambiguous license information stays visibly unknown: a request without one shows
"License: Unknown". An indexer category, filename, or search keyword must not be presented as
license verification.

## Preserve release materials

Keep accompanying license files, readmes, and credits. A multi-file release is imported as one
folder with all its wanted files, so they stay together in the library. Where a release
distinguishes the licenses for code, artwork, music, or the compiled game, retain those
distinctions in the source record rather than assigning one blanket label. Follow the release's applicable terms
when copying, adapting, or sharing it.

## Connect discovery to the collection

Configure suitable sources in Prowlarr, find a release in ROM Request, check its release page,
choose the platform, record the license, and request it. qBittorrent handles the transfer; RomM
organizes the imported game.

The operator's Prowlarr configuration determines what can appear in search. There is no license
allowlist, automated rights verification or moderation. The open-content focus describes the
intended use, not a guarantee about every search result.

## Examples in this repository

Use synthetic game names and files for tests. Any screenshots or demonstrations using
real games should link to the original release and its permission statement. Do not label a
real release Creative Commons or public domain without supporting source information.
