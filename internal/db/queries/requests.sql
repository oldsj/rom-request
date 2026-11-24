-- Handler queries take the owner, so one user never reads or changes another's requests. The
-- worker's queries (marked) act for the system and select by status or id.

-- name: ListRequests :many
SELECT * FROM requests
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT 500;

-- name: GetRequest :one
SELECT * FROM requests WHERE id = $1 AND user_id = $2;

-- name: CreateRequest :one
INSERT INTO requests (
    user_id, title, indexer, size_bytes, info_url, download_url, magnet_url,
    torrent_hash, torrent_ids, platform, license, source_url
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: CreateCatalogRequest :one
-- A game from the catalog; the worker finds the release.
INSERT INTO requests (
    user_id, title, indexer, size_bytes, info_url, download_url, magnet_url, platform,
    game_id, game_name, cover_id, catalog_slug, catalog_name, status
) VALUES (
    sqlc.arg(user_id), sqlc.arg(game_name), '', 0, '', '', '', sqlc.arg(platform),
    sqlc.arg(game_id), sqlc.arg(game_name), sqlc.arg(cover_id), sqlc.arg(catalog_slug),
    sqlc.arg(catalog_name), 'searching'
)
RETURNING *;

-- name: CatalogRequests :many
-- The latest request for each catalog game on a platform.
SELECT DISTINCT ON (game_id) * FROM requests
WHERE user_id = $1 AND platform = $2 AND game_id IS NOT NULL
ORDER BY game_id, created_at DESC
LIMIT 2000;

-- name: ClaimSearch :one
-- Worker: take a searching request's search. A new one (no attempts) is free; one whose search
-- started more than `stale` ago was left by a pod that died.
UPDATE requests
SET search_attempts = search_attempts + 1, updated_at = now(), step_started_at = now()
WHERE id = sqlc.arg(id) AND status = 'searching'
  AND (search_attempts = 0 OR step_started_at < now() - sqlc.arg(stale)::interval)
RETURNING *;

-- name: FoundRelease :one
-- Worker: searching -> queued with the release it picked. The submit step takes it from there.
UPDATE requests
SET status = 'queued', title = $2, indexer = $3, size_bytes = $4, info_url = $5,
    download_url = $6, magnet_url = $7, torrent_hash = $8, torrent_ids = $9,
    submit_attempts = 0, updated_at = now(), step_started_at = now()
WHERE id = $1 AND status = 'searching'
RETURNING *;

-- name: SearchAgain :one
-- Worker: a catalog request whose release link stopped working (indexers' links expire) goes
-- back to searching, at once: step_started_at is set back past the stale wait. Its search
-- attempts carry on counting, so this ends.
UPDATE requests
SET status = 'searching', title = game_name, indexer = '', size_bytes = 0, info_url = '',
    download_url = '', magnet_url = '', torrent_hash = '', torrent_ids = '{}', submit_attempts = 0,
    updated_at = now(), step_started_at = now() - interval '1 day'
WHERE id = $1 AND status = 'queued' AND game_id IS NOT NULL
RETURNING *;

-- name: RequestForTorrent :one
-- Any live request for a torrent, whoever made it.
SELECT id, user_id, status FROM requests
WHERE torrent_hash = $1 AND status <> 'failed'
LIMIT 1;

-- name: RetryRequest :one
-- Back to the queue from failed only; the worker or handler submits it again. A catalog
-- request that never found a release searches again.
UPDATE requests
SET status = CASE WHEN game_id IS NOT NULL AND torrent_hash = '' AND download_url = '' AND magnet_url = ''
                  THEN 'searching'::request_status ELSE 'queued'::request_status END,
    error_code = '', error_message = '', progress = 0, submit_attempts = 0, search_attempts = 0,
    updated_at = now(), step_started_at = now()
WHERE id = $1 AND user_id = $2 AND status = 'failed'
RETURNING *;

-- name: ActiveRequests :many
-- Worker.
SELECT * FROM requests
WHERE status IN ('searching', 'queued', 'downloading', 'importing')
ORDER BY created_at
LIMIT 200;

-- name: MarkSubmitted :one
-- Worker and handler: queued -> downloading, once qBittorrent has the torrent.
UPDATE requests
SET status = 'downloading', torrent_hash = $2, torrent_ids = $3, updated_at = now(),
    step_started_at = now()
WHERE id = $1 AND status = 'queued'
RETURNING *;

-- name: NoteSubmitAttempt :one
-- Worker: a submit failed for a reason that may pass; try again after a backoff.
UPDATE requests
SET submit_attempts = submit_attempts + 1, updated_at = now(), step_started_at = now()
WHERE id = $1 AND status = 'queued'
RETURNING submit_attempts;

-- name: SetProgress :exec
-- Worker.
UPDATE requests
SET progress = $2, updated_at = now()
WHERE id = $1 AND status = 'downloading' AND progress <> $2;

-- name: ClaimImport :one
-- Worker: take the import step with a new claim. Only one pod wins a downloading request; an
-- import whose holder stopped renewing it (crashed) is taken over after `stale`.
UPDATE requests
SET status = 'importing', progress = 1, import_claim = sqlc.arg(claim), updated_at = now(),
    step_started_at = now()
WHERE id = sqlc.arg(id)
  AND (status = 'downloading'
       OR (status = 'importing' AND step_started_at < now() - sqlc.arg(stale)::interval))
RETURNING *;

-- name: RenewImport :execrows
-- Worker: the holder is still importing. No rows: the claim was lost.
UPDATE requests SET step_started_at = now()
WHERE id = $1 AND import_claim = $2 AND status = 'importing';

-- name: ReleaseImport :execrows
-- Worker: give an unfinished import back (shutting down); the next round imports again.
UPDATE requests
SET status = 'downloading', import_claim = NULL, updated_at = now(), step_started_at = now()
WHERE id = $1 AND import_claim = $2 AND status = 'importing';

-- name: MarkImported :execrows
-- Worker, holding the claim. Prowlarr's links (which carry its key) aren't needed any more.
UPDATE requests
SET status = 'imported', imported_path = $3, imported_files = $4, imported_sha256 = $5,
    import_claim = NULL, download_url = '', magnet_url = '',
    imported_at = now(), updated_at = now(), step_started_at = now()
WHERE id = $1 AND import_claim = $2 AND status = 'importing';

-- name: FailImport :execrows
-- Worker, holding the claim.
UPDATE requests
SET status = 'failed', error_code = $3, error_message = $4, import_claim = NULL,
    updated_at = now(), step_started_at = now()
WHERE id = $1 AND import_claim = $2 AND status = 'importing';

-- name: FailImportNoGame :execrows
-- Worker, holding the claim: the download held no game file for its platform (only a web page,
-- images or metadata). A catalog request also forgets the release and queues its torrent for
-- removal, so Retry searches for another; a manual one keeps it (Retry downloads it again).
WITH failed AS (
    UPDATE requests r
    SET status = 'failed', error_code = sqlc.arg(error_code), error_message = sqlc.arg(error_message),
        import_claim = NULL, updated_at = now(), step_started_at = now(),
        title = CASE WHEN r.game_id IS NOT NULL THEN r.game_name ELSE r.title END,
        indexer = CASE WHEN r.game_id IS NOT NULL THEN '' ELSE r.indexer END,
        size_bytes = CASE WHEN r.game_id IS NOT NULL THEN 0 ELSE r.size_bytes END,
        info_url = CASE WHEN r.game_id IS NOT NULL THEN '' ELSE r.info_url END,
        download_url = CASE WHEN r.game_id IS NOT NULL THEN '' ELSE r.download_url END,
        magnet_url = CASE WHEN r.game_id IS NOT NULL THEN '' ELSE r.magnet_url END,
        torrent_hash = CASE WHEN r.game_id IS NOT NULL THEN '' ELSE r.torrent_hash END,
        torrent_ids = CASE WHEN r.game_id IS NOT NULL THEN '{}'::text[] ELSE r.torrent_ids END
    FROM requests old
    WHERE r.id = sqlc.arg(id) AND r.import_claim = sqlc.arg(import_claim) AND r.status = 'importing'
      AND old.id = r.id
    RETURNING r.game_id, old.torrent_ids AS old_torrent_ids
), queued AS (
    INSERT INTO torrent_removals (torrent_ids, save_path)
    SELECT failed.old_torrent_ids, sqlc.arg(save_path)::text FROM failed
    WHERE failed.game_id IS NOT NULL AND cardinality(failed.old_torrent_ids) > 0
    RETURNING torrent_removals.id
)
SELECT 1 FROM failed;

-- name: MarkFailed :execrows
-- Worker and handler: a searching, queued or downloading request -> failed, from the status
-- the caller saw, with a reason the user can act on.
UPDATE requests
SET status = 'failed', error_code = $2, error_message = $3, updated_at = now(),
    step_started_at = now()
WHERE id = $1 AND status = $4 AND status IN ('searching', 'queued', 'downloading');

-- name: RemoveRequest :one
-- Handler: delete a request (not while importing: the worker owns that step) and, unless it
-- was imported, queue its torrent for removal from qBittorrent. One statement, so a request
-- is never deleted without its cleanup.
WITH removed AS (
    DELETE FROM requests
    WHERE requests.id = sqlc.arg(id) AND requests.user_id = sqlc.arg(user_id)
      AND requests.status <> 'importing'
    RETURNING requests.id, requests.status, requests.torrent_ids
), queued AS (
    INSERT INTO torrent_removals (torrent_ids, save_path)
    SELECT removed.torrent_ids, sqlc.arg(save_path)::text FROM removed
    WHERE removed.status <> 'imported' AND cardinality(removed.torrent_ids) > 0
    RETURNING torrent_removals.id
)
-- removal_id is the nil uuid when nothing was queued.
SELECT removed.id,
       COALESCE((SELECT queued.id FROM queued), '00000000-0000-0000-0000-000000000000')::uuid AS removal_id
FROM removed;

-- name: QueueTorrentRemoval :one
-- Worker: a torrent added for a request that was removed meanwhile.
INSERT INTO torrent_removals (torrent_ids, save_path) VALUES ($1, $2) RETURNING *;

-- name: TorrentRemovals :many
-- Worker.
SELECT * FROM torrent_removals ORDER BY created_at LIMIT 100;

-- name: GetTorrentRemoval :one
SELECT * FROM torrent_removals WHERE id = $1;

-- name: DeleteTorrentRemoval :exec
-- Worker.
DELETE FROM torrent_removals WHERE id = $1;
