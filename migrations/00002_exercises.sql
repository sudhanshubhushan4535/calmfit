-- +goose Up
-- No secondary indexes: ~16 rows and exercise swaps are out of scope for v1
CREATE TABLE exercises (
    -- free-exercise-db id as the key, so re-seeding is idempotent
    id               text PRIMARY KEY,
    name             text NOT NULL,
    -- arrays default to empty, never NULL, so code doesn't handle two "no value" cases
    aliases          text[] NOT NULL DEFAULT '{}',
    -- nullable: free-exercise-db uses null for body-only exercises
    equipment        text,
    primary_muscles  text[] NOT NULL DEFAULT '{}',
    -- CHECK instead of an enum type: easy to extend with a migration, same safety
    muscle_size      text NOT NULL CHECK (muscle_size IN ('big', 'small')),
    rep_scheme       text NOT NULL CHECK (rep_scheme IN ('standard', 'high_rep')),
    -- must be positive or rep progression would stall or go backwards
    rep_step         int  NOT NULL CHECK (rep_step > 0),
    image_paths      text[] NOT NULL DEFAULT '{}',
    instructions     text[] NOT NULL DEFAULT '{}'
);

-- +goose Down
DROP TABLE exercises;
