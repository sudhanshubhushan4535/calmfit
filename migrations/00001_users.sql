-- +goose Up
CREATE TABLE users (
    -- gen_random_uuid() is built into PG13+, so no extension needed
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Google's stable subject id is our identity; UNIQUE also gives the login lookup index
    google_sub  text NOT NULL UNIQUE,
    -- Google always returns an email; not unique because it can change while sub stays fixed
    email       text NOT NULL,
    -- nullable: the user may not share a display name
    name        text,
    -- timestamptz so times are unambiguous across zones
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- soft delete: NULL means active, keeps history recoverable
    deleted_at  timestamptz
);

-- +goose Down
DROP TABLE users;
