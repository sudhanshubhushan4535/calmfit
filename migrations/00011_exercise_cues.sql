-- +goose Up
-- Short coaching cues shown with the instructions; seeded, pending coach review.
-- Defaults to empty, never NULL, like the other text[] columns; `make seed` fills it.
ALTER TABLE exercises ADD COLUMN cues text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE exercises DROP COLUMN cues;
