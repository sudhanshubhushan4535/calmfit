-- +goose Up
-- Product analytics in our own database, no third-party tracker (D48). Only the six spec events.
-- Written in the same transaction as the business write they describe, except next_target_viewed,
-- which the app posts when it shows the next targets.
CREATE TABLE analytics_events (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (name IN ('signup_completed', 'first_workout_completed',
                     'second_workout_completed', 'workout_completed', 'next_target_viewed', 'plate_checked')),
    occurred_at  timestamptz NOT NULL DEFAULT now()
);
-- Account deletion and export read per user; the pilot queries join per user and event.
-- Pilot scale is ~20 users, so this one index is all the table needs.
CREATE INDEX analytics_events_user_name_idx ON analytics_events (user_id, name, occurred_at);

-- +goose Down
DROP TABLE analytics_events;
