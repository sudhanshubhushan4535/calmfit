-- +goose Up
CREATE TABLE sessions (
    -- SHA-256 of the cookie token: a DB leak can't be replayed as a cookie; PK index serves the per-request lookup
    id_hash     bytea PRIMARY KEY,
    -- CASCADE: hard-deleting a user removes their sessions
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- checked on every request; no index since v1 has no expired-session cleanup job
    expires_at  timestamptz NOT NULL
);

-- Postgres doesn't index FKs automatically; needed for the cascade on user delete
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

-- +goose Down
DROP TABLE sessions;
