// Package worker consumes workout-events and maintains derived stats.
//
// Delivery is at-least-once: a record's offset is committed only after its DB transaction has
// committed. Inside that transaction the event_id is inserted into processed_events first; if it
// is already there the event is a duplicate and nothing else happens. So a redelivered event
// (relay re-publish, crash before the offset commit, rebalance) is applied exactly once.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
)

// Group is the consumer group of the stats worker; run 2 instances in it.
const Group = "calmfit-stats"

// Outcome is what happened to one record.
type Outcome string

const (
	Applied     Outcome = "applied"
	Duplicate   Outcome = "duplicate"    // event_id already in processed_events
	SkippedUser Outcome = "skipped_user" // the user is deleted or unknown
	Rejected    Outcome = "rejected"     // the event failed (see the error)
)

// NewConsumer returns a group consumer with manual commits. BlockRebalanceOnPoll keeps
// partitions assigned between a poll and its commit, so a rebalance can't hand a half-processed
// batch to another instance.
func NewConsumer(brokers []string, group, topic string, opts ...kgo.Opt) (*kgo.Client, error) {
	base := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		// The same client produces to the DLQ; a dead broker fails that produce instead of hanging.
		kgo.RecordDeliveryTimeout(10 * time.Second),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), // a new group reads the full history
		kgo.OnPartitionsAssigned(func(_ context.Context, _ *kgo.Client, m map[string][]int32) {
			log.Printf("worker: assigned %v", m)
		}),
		kgo.OnPartitionsRevoked(func(_ context.Context, _ *kgo.Client, m map[string][]int32) {
			log.Printf("worker: revoked %v", m)
		}),
	}
	return kgo.NewClient(append(base, opts...)...)
}

// Worker applies events from Client to DB.
type Worker struct {
	DB       *pgxpool.Pool
	Client   *kgo.Client
	Handlers map[Key]HandlerFunc

	DLQTopic    string        // required: where records that keep failing go
	MaxAttempts int           // 0 means DefaultMaxAttempts
	BaseBackoff time.Duration // 0 means DefaultBaseBackoff; doubles after each failed attempt

	// AfterDBCommit, if set, runs after a record's DB transaction commits and before its offset
	// is committed. Returning an error stops Run without committing anything from the batch,
	// which is how tests simulate a crash in that window.
	AfterDBCommit func(*kgo.Record) error
}

// Run polls, applies and commits until ctx is cancelled (returns nil) or a record can't be
// applied *or* sent to the DLQ (returns the error). In that case the offsets of the records
// already handled are committed and the failing one is not, so after a restart it comes again.
//
// Shutdown is graceful: cancelling ctx lets the record in flight finish and its batch prefix be
// committed (work uses a context that isn't cancelled), then Run returns between records.
func (w *Worker) Run(ctx context.Context) error {
	if w.DLQTopic == "" {
		return errors.New("worker: DLQTopic is required")
	}
	work := context.WithoutCancel(ctx)
	for {
		fs := w.Client.PollRecords(ctx, 100)
		if ctx.Err() != nil || fs.IsClientClosed() {
			w.Client.AllowRebalance()
			return nil
		}
		fs.EachError(func(topic string, p int32, err error) {
			log.Printf("worker: fetch %s/%d: %v", topic, p, err)
		})
		recs := fs.Records()
		done := 0
		var runErr error
		for _, r := range recs {
			if ctx.Err() != nil {
				break // shutting down: commit what's done, leave the rest for the next owner
			}
			if err := w.Handle(ctx, work, r); err != nil {
				if !errors.Is(err, errShutdown) {
					runErr = err
				}
				break // on shutdown the record isn't committed, so the next owner retries it
			}
			if w.AfterDBCommit != nil {
				if err := w.AfterDBCommit(r); err != nil {
					w.Client.AllowRebalance()
					return err // simulated crash: nothing from this batch is committed
				}
			}
			done++
		}
		cerr := w.commit(work, recs[:done])
		w.Client.AllowRebalance()
		if runErr != nil || cerr != nil {
			return errors.Join(runErr, cerr)
		}
	}
}

func (w *Worker) commit(ctx context.Context, recs []*kgo.Record) error {
	if len(recs) == 0 {
		return nil
	}
	if err := w.Client.CommitRecords(ctx, recs...); err != nil {
		return fmt.Errorf("worker: commit offsets: %w", err)
	}
	return nil
}

// Defaults for retrying a transient failure.
const (
	DefaultMaxAttempts = 5
	DefaultBaseBackoff = 200 * time.Millisecond // waits 200, 400, 800, 1600 ms (±jitter): about 3 s in all
)

var errShutdown = errors.New("worker: shutting down")

// Handle applies one record. A permanent failure goes to the DLQ at once; a transient one is
// retried up to MaxAttempts with exponential backoff and then goes to the DLQ, so one bad record
// can't block its partition. It returns an error only if the DLQ produce fails (the record must
// not be committed then) or ctx is cancelled during a backoff (errShutdown).
func (w *Worker) Handle(ctx, work context.Context, r *kgo.Record) error {
	maxAttempts := DefaultMaxAttempts
	if w.MaxAttempts > 0 {
		maxAttempts = w.MaxAttempts
	}
	backoff := DefaultBaseBackoff
	if w.BaseBackoff > 0 {
		backoff = w.BaseBackoff
	}
	where := fmt.Sprintf("%s/%d@%d", r.Topic, r.Partition, r.Offset)

	var err error
	attempt := 1
	for ; ; attempt++ {
		var out Outcome
		var eventID string
		out, eventID, err = w.Apply(work, r.Value)
		if err == nil {
			log.Printf("worker: %s event %s %s", where, eventID, out)
			eventsTotal.WithLabelValues(string(out)).Inc()
			return nil
		}
		if IsPermanent(err) || attempt == maxAttempts {
			break
		}
		log.Printf("worker: %s event %s attempt %d/%d failed: %v", where, eventID, attempt, maxAttempts, err)
		retriesTotal.Inc()
		// Full jitter in [d/2, d): instances retrying the same outage don't hit the DB in lockstep.
		d := backoff << (attempt - 1)
		d = d/2 + rand.N(d/2)
		select {
		case <-ctx.Done():
			return errShutdown
		case <-time.After(d):
		}
	}

	log.Printf("worker: %s → DLQ after %d attempt(s): %v", where, attempt, err)
	if perr := w.Client.ProduceSync(work, w.dlqRecord(r, err, attempt)).FirstErr(); perr != nil {
		return fmt.Errorf("worker: %s: DLQ produce failed, not committing: %w (cause: %v)", where, perr, err)
	}
	eventsTotal.WithLabelValues(string(OutcomeDLQ)).Inc()
	return nil
}

// DLQ record headers.
const (
	HeaderError        = "error"
	HeaderAttempts     = "attempts"
	HeaderSrcTopic     = "src-topic"
	HeaderSrcPartition = "src-partition"
	HeaderSrcOffset    = "src-offset"
	HeaderFailedAt     = "failed-at"
)

// dlqRecord keeps the original key, value and headers and adds why and where it failed.
func (w *Worker) dlqRecord(r *kgo.Record, cause error, attempts int) *kgo.Record {
	hs := append([]kgo.RecordHeader(nil), r.Headers...)
	hs = append(hs,
		kgo.RecordHeader{Key: HeaderError, Value: []byte(cause.Error())},
		kgo.RecordHeader{Key: HeaderAttempts, Value: []byte(strconv.Itoa(attempts))},
		kgo.RecordHeader{Key: HeaderSrcTopic, Value: []byte(r.Topic)},
		kgo.RecordHeader{Key: HeaderSrcPartition, Value: []byte(strconv.Itoa(int(r.Partition)))},
		kgo.RecordHeader{Key: HeaderSrcOffset, Value: []byte(strconv.FormatInt(r.Offset, 10))},
		kgo.RecordHeader{Key: HeaderFailedAt, Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	)
	return &kgo.Record{Topic: w.DLQTopic, Key: r.Key, Value: r.Value, Headers: hs}
}

// Apply decodes one record value and applies it in a single DB transaction:
// dedupe on event_id → deleted-user guard → handler. The returned eventID may be empty when the
// value can't be decoded.
func (w *Worker) Apply(ctx context.Context, value []byte) (Outcome, string, error) {
	e, err := events.Decode(value)
	if err != nil {
		return Rejected, e.EventID, Permanent(err)
	}
	h, ok := w.Handlers[Key{e.Type, e.Version}]
	if !ok {
		return Rejected, e.EventID, Permanent(fmt.Errorf("no handler for %s v%d", e.Type, e.Version))
	}

	out := Applied
	err = pgx.BeginFunc(ctx, w.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO processed_events (event_id) VALUES ($1::uuid) ON CONFLICT DO NOTHING`, e.EventID)
		if err != nil {
			return fmt.Errorf("record processed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			out = Duplicate
			return nil
		}
		// FOR SHARE blocks account deletion (which locks the row FOR UPDATE) until this commits,
		// and vice versa: an event can't write stats for a user deleted mid-transaction.
		// user.deleted itself always runs: it only removes rows.
		if e.Type != events.TypeUserDeleted {
			var deleted bool
			err := tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM users WHERE id = $1::uuid FOR SHARE`, e.UserID).Scan(&deleted)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && deleted) {
				out = SkippedUser
				return nil // still recorded as processed, so a replay skips it too
			}
			if err != nil {
				return fmt.Errorf("check user: %w", err)
			}
		}
		return h(ctx, tx, e)
	})
	if err != nil {
		return Rejected, e.EventID, err
	}
	return out, e.EventID, nil
}
