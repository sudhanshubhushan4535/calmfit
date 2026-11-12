-- +goose Up
-- Account deletion leaves a tombstone: deleted_at set, email/name blanked and google_sub NULL, so
-- the same Google account can sign up again as a brand-new user. UNIQUE still allows many NULLs.
-- The tombstone itself stays so FKs never dangle and the worker can tell "deleted" from "unknown".
UPDATE users SET google_sub = NULL, email = '', name = NULL WHERE deleted_at IS NOT NULL;

ALTER TABLE users
    ALTER COLUMN google_sub DROP NOT NULL,
    -- active users always have a sub; deleted users never do
    ADD CONSTRAINT users_tombstone_check CHECK ((deleted_at IS NULL) = (google_sub IS NOT NULL));

-- +goose Down
-- Fails once a tombstone exists (its google_sub is NULL): the original sub is gone by design.
ALTER TABLE users
    DROP CONSTRAINT users_tombstone_check,
    ALTER COLUMN google_sub SET NOT NULL;
