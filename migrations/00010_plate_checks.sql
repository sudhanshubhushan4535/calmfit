-- +goose Up
CREATE TABLE plate_checks (
    -- CASCADE: hard-deleting a user removes their plate checks
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- the user's local calendar date (config timezone), not a UTC timestamp
    local_date  date NOT NULL,
    meal        text NOT NULL CHECK (meal IN ('breakfast', 'lunch', 'dinner')),
    protein     boolean NOT NULL,
    fiber       boolean NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    -- one row per meal: the upsert target. Also serves per-user date-range reads; no extra index
    PRIMARY KEY (user_id, local_date, meal)
);

-- +goose Down
DROP TABLE plate_checks;
