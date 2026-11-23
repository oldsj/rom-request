-- +goose Up
-- uuidv7() is built in from PostgreSQL 18: time-ordered ids index better than random ones.
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Stored lower-cased by the server; the check makes sure nothing else writes it otherwise.
    email         text NOT NULL UNIQUE CHECK (email = lower(email)),
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Server-side sessions. Only a SHA-256 of the cookie's token is stored, so a database leak
-- doesn't hand out live sessions.
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE notes (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      text NOT NULL,
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notes_user_id_created_at_idx ON notes (user_id, created_at DESC);

-- +goose Down
DROP TABLE notes;
DROP TABLE sessions;
DROP TABLE users;
