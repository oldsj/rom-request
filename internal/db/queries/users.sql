-- name: CreateUser :one
INSERT INTO users (email, password_hash)
VALUES ($1, $2)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByOIDCSubject :one
SELECT * FROM users WHERE oidc_subject = $1;

-- name: CreateOIDCUser :one
-- No password: such an account signs in only through OIDC.
INSERT INTO users (email, password_hash, oidc_subject)
VALUES ($1, '', $2)
RETURNING *;

-- name: LinkOIDCSubject :one
-- Links an existing account (matched by a verified email) that isn't linked yet.
UPDATE users SET oidc_subject = $2
WHERE id = $1 AND oidc_subject IS NULL
RETURNING *;
