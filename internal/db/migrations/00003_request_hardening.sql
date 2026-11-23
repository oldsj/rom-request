-- +goose Up
-- Every id qBittorrent may know a torrent by: a hybrid (v1 + v2) torrent is its v1 hash on
-- libtorrent 1.2 builds and its truncated v2 hash on libtorrent 2.x builds.
ALTER TABLE requests ADD COLUMN torrent_ids text[] NOT NULL DEFAULT '{}';
UPDATE requests SET torrent_ids = ARRAY[torrent_hash] WHERE torrent_hash <> '';
-- Failed submit attempts since the request was last queued, for backoff.
ALTER TABLE requests ADD COLUMN submit_attempts integer NOT NULL DEFAULT 0;
-- The import step's owner. Only the holder may finish or release an import.
ALTER TABLE requests ADD COLUMN import_claim uuid;

-- Torrents to remove from qBittorrent (with their files) for requests that were removed, kept
-- until qBittorrent confirms, so removing a request works while qBittorrent is down.
CREATE TABLE torrent_removals (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    torrent_ids text[] NOT NULL,
    save_path   text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE torrent_removals;
ALTER TABLE requests DROP COLUMN import_claim;
ALTER TABLE requests DROP COLUMN submit_attempts;
ALTER TABLE requests DROP COLUMN torrent_ids;
