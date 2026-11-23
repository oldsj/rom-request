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
    torrent_hash, platform, license, source_url
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: RequestForTorrent :one
-- Any live request for a torrent, whoever made it.
SELECT id, user_id, status FROM requests
WHERE torrent_hash = $1 AND status <> 'failed'
LIMIT 1;

-- name: RetryRequest :one
-- Back to the queue from failed only; the worker or handler submits it again.
UPDATE requests
SET status = 'queued', error_code = '', error_message = '', progress = 0,
    updated_at = now(), step_started_at = now()
WHERE id = $1 AND user_id = $2 AND status = 'failed'
RETURNING *;

-- name: DeleteRequest :one
-- Not while importing: the worker owns that step until it ends.
DELETE FROM requests
WHERE id = $1 AND user_id = $2 AND status <> 'importing'
RETURNING *;

-- name: ActiveRequests :many
-- Worker.
SELECT * FROM requests
WHERE status IN ('queued', 'downloading', 'importing')
ORDER BY created_at
LIMIT 200;

-- name: MarkSubmitted :one
-- Worker and handler: queued -> downloading, once qBittorrent has the torrent.
UPDATE requests
SET status = 'downloading', torrent_hash = $2, updated_at = now(), step_started_at = now()
WHERE id = $1 AND status = 'queued'
RETURNING *;

-- name: SetProgress :exec
-- Worker.
UPDATE requests
SET progress = $2, updated_at = now()
WHERE id = $1 AND status = 'downloading' AND progress <> $2;

-- name: ClaimImport :one
-- Worker: take the import step. Only one pod wins a downloading request; an import left
-- behind by a crashed pod is reclaimed after `stale`.
UPDATE requests
SET status = 'importing', progress = 1, updated_at = now(), step_started_at = now()
WHERE id = sqlc.arg(id)
  AND (status = 'downloading'
       OR (status = 'importing' AND step_started_at < now() - sqlc.arg(stale)::interval))
RETURNING *;

-- name: MarkImported :exec
-- Worker.
UPDATE requests
SET status = 'imported', imported_path = $2, imported_files = $3, imported_sha256 = $4,
    imported_at = now(), updated_at = now(), step_started_at = now()
WHERE id = $1 AND status = 'importing';

-- name: MarkFailed :exec
-- Worker and handler: any live step -> failed, with a reason the user can act on.
UPDATE requests
SET status = 'failed', error_code = $2, error_message = $3, updated_at = now(),
    step_started_at = now()
WHERE id = $1 AND status IN ('queued', 'downloading', 'importing');
