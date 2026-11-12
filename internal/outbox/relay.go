package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
)

// Relay defaults.
const (
	DefaultBatchSize    = 100
	DefaultInterval     = 500 * time.Millisecond
	DefaultRetention    = 7 * 24 * time.Hour // published rows are kept this long for debugging
	DefaultCleanupEvery = time.Minute
)

// NewProducer returns a Kafka client for the relay: acks from all in-sync replicas and the
// idempotent producer (both franz-go defaults, set here so they're visible), and a bounded
// delivery timeout so a dead broker fails the batch instead of hanging the relay.
func NewProducer(brokers []string) (*kgo.Client, error) {
	return kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordDeliveryTimeout(10*time.Second),
	)
}

// Relay polls the outbox and publishes rows to Kafka, keyed by user_id.
// Run one instance: SKIP LOCKED makes more safe, but two relays could publish one user's events
// out of order.
type Relay struct {
	DB           *pgxpool.Pool
	Client       *kgo.Client
	Topic        string
	BatchSize    int           // 0 means DefaultBatchSize
	Interval     time.Duration // 0 means DefaultInterval
	Retention    time.Duration // 0 means DefaultRetention
	CleanupEvery time.Duration // 0 means DefaultCleanupEvery
}

func orDefault[T int | time.Duration](v, d T) T {
	if v == 0 {
		return d
	}
	return v
}

// PublishBatch publishes up to BatchSize of the oldest unpublished rows and marks them published,
// in one transaction. Rows locked by another relay are skipped. If the produce fails, the
// transaction rolls back and the rows stay unpublished for the next poll.
func (r *Relay) PublishBatch(ctx context.Context) (int, error) {
	var n int
	err := pgx.BeginFunc(ctx, r.DB, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, event_id::text, user_id::text, type, version, payload, created_at
			FROM outbox WHERE published_at IS NULL
			ORDER BY id LIMIT $1
			FOR UPDATE SKIP LOCKED`, orDefault(r.BatchSize, DefaultBatchSize))
		if err != nil {
			return fmt.Errorf("select: %w", err)
		}
		var ids []int64
		var recs []*kgo.Record
		for rows.Next() {
			var id int64
			var e events.Envelope
			if err := rows.Scan(&id, &e.EventID, &e.UserID, &e.Type, &e.Version, &e.Data, &e.OccurredAt); err != nil {
				rows.Close()
				return fmt.Errorf("scan: %w", err)
			}
			value, err := json.Marshal(e)
			if err != nil {
				rows.Close()
				return fmt.Errorf("marshal event %s: %w", e.EventID, err)
			}
			ids = append(ids, id)
			recs = append(recs, &kgo.Record{Topic: r.Topic, Key: []byte(e.UserID), Value: value})
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("rows: %w", err)
		}
		if len(recs) == 0 {
			return nil
		}
		// Same key → same partition, and the idempotent producer keeps per-partition order.
		if err := r.Client.ProduceSync(ctx, recs...).FirstErr(); err != nil {
			return fmt.Errorf("produce: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
			return fmt.Errorf("mark published: %w", err)
		}
		n = len(ids)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("outbox relay: %w", err)
	}
	return n, nil
}

// Cleanup deletes rows published before the cutoff. Unpublished rows are never deleted.
func (r *Relay) Cleanup(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.DB.Exec(ctx, `DELETE FROM outbox WHERE published_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("outbox cleanup: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Run publishes until ctx is cancelled. A full batch polls again immediately; errors are logged
// and retried on the next tick, so a broker outage only delays events.
func (r *Relay) Run(ctx context.Context) error {
	interval := orDefault(r.Interval, DefaultInterval)
	batch := orDefault(r.BatchSize, DefaultBatchSize)
	cleanupEvery := orDefault(r.CleanupEvery, DefaultCleanupEvery)
	retention := orDefault(r.Retention, DefaultRetention)
	var lastCleanup time.Time

	for {
		n, err := r.PublishBatch(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("relay: %v", err)
		} else if n > 0 {
			log.Printf("relay: published %d events", n)
		}

		if time.Since(lastCleanup) >= cleanupEvery {
			if deleted, err := r.Cleanup(ctx, time.Now().Add(-retention)); err != nil && ctx.Err() == nil {
				log.Printf("relay: %v", err)
			} else if deleted > 0 {
				log.Printf("relay: cleaned up %d published events", deleted)
			}
			lastCleanup = time.Now()
		}

		if err == nil && n == batch {
			if ctx.Err() != nil {
				return nil
			}
			continue // more rows are probably waiting
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
