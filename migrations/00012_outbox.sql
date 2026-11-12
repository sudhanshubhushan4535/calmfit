-- +goose Up
-- Transactional outbox: a row is inserted in the same transaction as the domain write it describes,
-- so an event exists if and only if that write committed. The relay publishes rows to Kafka.
CREATE TABLE outbox (
    -- bigserial gives the relay a stable publish order (per user, events stay in commit order)
    id            bigserial PRIMARY KEY,
    -- the consumers' idempotency key; generated here so a re-published row keeps the same id
    event_id      uuid NOT NULL DEFAULT gen_random_uuid(),
    -- Kafka key; no FK, so a user.deleted event outlives the user's other rows
    user_id       uuid NOT NULL,
    type          text NOT NULL,
    version       int  NOT NULL CHECK (version > 0),
    payload       jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- NULL until the broker has acknowledged the record
    published_at  timestamptz
);

-- Serves the relay's "oldest unpublished first" query and stays tiny: published rows drop out.
-- No other index: the 7-day cleanup scans a table that holds about a week of pilot traffic.
CREATE INDEX outbox_unpublished_idx ON outbox (id) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE outbox;
