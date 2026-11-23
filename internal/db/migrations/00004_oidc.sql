-- +goose Up
-- The OIDC provider's subject for a user who signs in through it. An account made through OIDC
-- has no password: password_hash is empty, and password sign-in refuses it.
ALTER TABLE users ADD COLUMN oidc_subject text UNIQUE;

-- A sign-in started at the provider and not yet back. Keyed by a hash of the state, which the
-- browser also holds in a cookie, so a callback only completes in the browser that started it.
CREATE TABLE oidc_logins (
    state_hash bytea PRIMARY KEY,
    nonce      text NOT NULL,
    verifier   text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX oidc_logins_expires_at_idx ON oidc_logins (expires_at);

-- +goose Down
DROP TABLE oidc_logins;
ALTER TABLE users DROP COLUMN oidc_subject;
