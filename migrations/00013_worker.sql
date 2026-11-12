-- +goose Up
-- Idempotency for the stats worker: the event_id is inserted in the same transaction as the side
-- effect, so an event is applied at most once however often Kafka delivers it.
-- The PK is the dedupe check; no other index.
CREATE TABLE processed_events (
    event_id      uuid PRIMARY KEY,
    processed_at  timestamptz NOT NULL DEFAULT now()
);

-- Derived from workout.logged events. Never written by the API; rebuilt by replaying the topic.
CREATE TABLE weekly_stats (
    -- CASCADE: hard-deleting a user removes their derived rows
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Monday of the week (config timezone) the workout was completed in, like the progress charts
    week_start  date NOT NULL CHECK (extract(isodow FROM week_start) = 1),
    workouts    int  NOT NULL CHECK (workouts >= 0),
    sets        int  NOT NULL CHECK (sets >= 0),
    -- sum of weight_g × reps over the week's working sets
    volume_g    bigint NOT NULL CHECK (volume_g >= 0),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    -- the increment's upsert target; also serves per-user reads and the cascade
    PRIMARY KEY (user_id, week_start)
);

-- +goose Down
DROP TABLE weekly_stats;
DROP TABLE processed_events;
