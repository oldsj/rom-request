-- +goose Up
-- A limited user (a child): browses and requests catalog games not rated mature, and nothing
-- else (no free-text release search). Set from the OIDC groups at each sign-in.
ALTER TABLE users ADD COLUMN limited boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN limited;
