-- Prowlarr results between a search and a request. Every read takes the owner.

-- name: InsertSearchResult :one
INSERT INTO search_results (
    user_id, title, indexer, size_bytes, seeders, leechers, published_at,
    info_url, download_url, magnet_url, info_hash, categories, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING id;

-- name: GetSearchResult :one
SELECT * FROM search_results
WHERE id = $1 AND user_id = $2 AND expires_at > now();

-- name: DeleteExpiredSearchResults :execrows
DELETE FROM search_results WHERE expires_at <= now();
