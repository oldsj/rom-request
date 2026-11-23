-- +goose Up
-- Requests made from the catalog: a game, not yet a release. The worker searches Prowlarr for
-- one (status searching), then the request continues as any other (queued, downloading, ...).
ALTER TYPE request_status ADD VALUE IF NOT EXISTS 'searching' BEFORE 'queued';

-- IGDB's id and name for the game, its cover, and IGDB's slug and name for the platform (to
-- recognise the platform in release names). Null and empty for a request of a search result.
ALTER TABLE requests ADD COLUMN game_id bigint;
ALTER TABLE requests ADD COLUMN game_name text NOT NULL DEFAULT '';
ALTER TABLE requests ADD COLUMN cover_id text NOT NULL DEFAULT '';
ALTER TABLE requests ADD COLUMN catalog_slug text NOT NULL DEFAULT '';
ALTER TABLE requests ADD COLUMN catalog_name text NOT NULL DEFAULT '';
-- Searches started for the request since it was last (re)queued; a worker claims a search by
-- bumping it, and takes over one whose pod died after a while.
ALTER TABLE requests ADD COLUMN search_attempts integer NOT NULL DEFAULT 0;

-- One live request per game and platform for each user.
CREATE UNIQUE INDEX requests_user_game_idx ON requests (user_id, platform, game_id)
    WHERE game_id IS NOT NULL AND status <> 'failed';

-- +goose Down
-- PostgreSQL can't drop an enum value: searching requests become failed, and the value stays.
UPDATE requests SET status = 'failed', error_code = 'no_release',
    error_message = 'The search was stopped.' WHERE status::text = 'searching';
DROP INDEX requests_user_game_idx;
ALTER TABLE requests DROP COLUMN search_attempts;
ALTER TABLE requests DROP COLUMN catalog_name;
ALTER TABLE requests DROP COLUMN catalog_slug;
ALTER TABLE requests DROP COLUMN cover_id;
ALTER TABLE requests DROP COLUMN game_name;
ALTER TABLE requests DROP COLUMN game_id;
