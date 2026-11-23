-- OIDC sign-ins in progress.

-- name: CreateOIDCLogin :exec
INSERT INTO oidc_logins (state_hash, nonce, verifier, expires_at)
VALUES ($1, $2, $3, $4);

-- name: TakeOIDCLogin :one
-- Each sign-in completes at most once.
DELETE FROM oidc_logins
WHERE state_hash = $1 AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredOIDCLogins :execrows
DELETE FROM oidc_logins WHERE expires_at <= now();
