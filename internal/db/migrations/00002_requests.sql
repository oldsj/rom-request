-- +goose Up
-- The template's example feature; ROM requests replace it.
DROP TABLE notes;

-- Prowlarr results, kept server-side between a search and a request: the download link carries
-- Prowlarr's API key, so the browser only ever sees the row's id. Rows expire after an hour.
CREATE TABLE search_results (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title        text NOT NULL,
    indexer      text NOT NULL,
    size_bytes   bigint NOT NULL,
    seeders      integer,
    leechers     integer,
    published_at timestamptz,
    info_url     text NOT NULL,
    download_url text NOT NULL,
    magnet_url   text NOT NULL,
    info_hash    text NOT NULL,
    categories   text[] NOT NULL,
    expires_at   timestamptz NOT NULL
);
CREATE INDEX search_results_user_id_idx ON search_results (user_id);
CREATE INDEX search_results_expires_at_idx ON search_results (expires_at);

CREATE TYPE request_status AS ENUM ('queued', 'downloading', 'importing', 'imported', 'failed');

CREATE TABLE requests (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title           text NOT NULL,
    indexer         text NOT NULL,
    size_bytes      bigint NOT NULL,
    info_url        text NOT NULL,
    -- Server-only: never returned by the API (they may carry Prowlarr's API key).
    download_url    text NOT NULL,
    magnet_url      text NOT NULL,
    -- The torrent's id in qBittorrent: the v1 info-hash, or the truncated v2 hash of a v2-only
    -- torrent. Lower-case hex. Empty until the torrent has been resolved.
    torrent_hash    text NOT NULL DEFAULT '',
    platform        text NOT NULL,
    -- What the requester recorded about the release; empty means unknown.
    license         text NOT NULL DEFAULT '',
    source_url      text NOT NULL DEFAULT '',
    status          request_status NOT NULL DEFAULT 'queued',
    progress        real NOT NULL DEFAULT 0 CHECK (progress >= 0 AND progress <= 1),
    error_code      text NOT NULL DEFAULT '',
    error_message   text NOT NULL DEFAULT '',
    -- Library-relative path of the imported file or folder, e.g. GBA/Game.gba.
    imported_path   text NOT NULL DEFAULT '',
    imported_files  integer NOT NULL DEFAULT 0,
    -- SHA-256 of the imported file, for single-file games.
    imported_sha256 text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    -- When the current step started; a worker reclaims a step left behind by a crashed pod.
    step_started_at timestamptz NOT NULL DEFAULT now(),
    imported_at     timestamptz
);
CREATE INDEX requests_user_id_created_at_idx ON requests (user_id, created_at DESC);
CREATE INDEX requests_active_idx ON requests (status) WHERE status IN ('queued', 'downloading', 'importing');
-- One live request per torrent, across users: two imports of one download would conflict.
CREATE UNIQUE INDEX requests_torrent_hash_idx ON requests (torrent_hash)
    WHERE torrent_hash <> '' AND status <> 'failed';

-- +goose Down
DROP TABLE requests;
DROP TYPE request_status;
DROP TABLE search_results;
CREATE TABLE notes (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      text NOT NULL,
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notes_user_id_created_at_idx ON notes (user_id, created_at DESC);
