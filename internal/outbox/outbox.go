// Package outbox implements the transactional outbox: Enqueue writes an event row inside the
// caller's transaction, and Relay publishes unpublished rows to Kafka.
//
// The relay is at-least-once: if the broker acks a batch but the commit marking it published
// fails, the batch is published again. Consumers dedupe on event_id.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Enqueue inserts one event into the outbox. It takes a pgx.Tx, not a pool, so an event can only
// be written as part of a transaction: it commits if and only if the domain write commits.
func Enqueue(ctx context.Context, tx pgx.Tx, userID, eventType string, version int, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("outbox: marshal %s: %w", eventType, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox (user_id, type, version, payload) VALUES ($1::uuid, $2, $3, $4)`,
		userID, eventType, version, payload); err != nil {
		return fmt.Errorf("outbox: insert %s: %w", eventType, err)
	}
	return nil
}
