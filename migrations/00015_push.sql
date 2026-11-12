-- +goose Up
-- Web Push subscriptions (D47). One row per browser: the endpoint is unique, and the latest user to
-- subscribe from that browser owns it. CASCADE is a backstop; account deletion deletes these rows itself.
CREATE TABLE push_subscriptions (
    endpoint    text PRIMARY KEY CHECK (length(endpoint) BETWEEN 1 AND 1024),
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    p256dh      text NOT NULL,
    auth        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- The sender reads per user, and account deletion deletes per user.
CREATE INDEX push_subscriptions_user_id_idx ON push_subscriptions (user_id);

-- At most one reminder per user per local day (D47). The row is written before sending, so a
-- second cron run the same day sends nothing.
CREATE TABLE reminder_log (
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    local_date  date NOT NULL,
    sent_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, local_date)
);

-- +goose Down
DROP TABLE reminder_log;
DROP TABLE push_subscriptions;
