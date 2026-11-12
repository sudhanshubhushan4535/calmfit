package outbox_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/outbox"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

// newUser inserts a bare user (cleaned up with its outbox rows by SubPrefix) and returns its id.
func newUser(t *testing.T, db *pgxpool.Pool, sub string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(),
		`INSERT INTO users (google_sub, email) VALUES ($1, $1 || '@example.com') RETURNING id::text`, sub).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func enqueue(t *testing.T, db *pgxpool.Pool, uid string, n int) {
	t.Helper()
	err := pgx.BeginFunc(context.Background(), db, func(tx pgx.Tx) error {
		for i := range n {
			if err := outbox.Enqueue(context.Background(), tx, uid, events.TypePlateChecked, events.V1,
				events.PlateChecked{LocalDate: "2026-11-12", Meal: "lunch", Protein: i%2 == 0}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func unpublished(t *testing.T, db *pgxpool.Pool, uid string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox WHERE user_id = $1::uuid AND published_at IS NULL`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func producer(t *testing.T, brokers []string) *kgo.Client {
	t.Helper()
	cl, err := outbox.NewProducer(brokers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cl.Close)
	return cl
}

// recordsFor decodes the topic's records keyed by uid. The test DB is shared with other packages'
// tests, so the relay may also publish their rows; those are ignored.
func recordsFor(t *testing.T, k testutil.Kafka, uid string) []events.Envelope {
	t.Helper()
	var out []events.Envelope
	for _, r := range testutil.ReadAll(t, k.Brokers, k.Topic) {
		if string(r.Key) != uid {
			continue
		}
		e, err := events.Decode(r.Value)
		if err != nil {
			t.Fatalf("published record is not a valid envelope: %v", err)
		}
		out = append(out, e)
	}
	return out
}

func TestRelayPublishesKeyedByUserInOrder(t *testing.T) {
	db := testutil.DB(t)
	k := testutil.KafkaTopics(t)
	uid := newUser(t, db, testutil.SubPrefix(t, db)+"relay")
	enqueue(t, db, uid, 3)

	r := &outbox.Relay{DB: db, Client: producer(t, k.Brokers), Topic: k.Topic}
	for unpublished(t, db, uid) > 0 {
		if _, err := r.PublishBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	got := recordsFor(t, k, uid)
	if len(got) != 3 {
		t.Fatalf("published %d records for the user, want 3", len(got))
	}
	rows, err := db.Query(context.Background(),
		`SELECT event_id::text FROM outbox WHERE user_id = $1::uuid ORDER BY id`, uid)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range got {
		// Same key, same partition: Kafka order matches outbox (commit) order.
		if e.EventID != ids[i] || e.UserID != uid || e.Type != events.TypePlateChecked || e.Version != 1 {
			t.Errorf("record %d = %+v, want event %s", i, e, ids[i])
		}
	}
}

// Two relays polling at once never publish the same row twice: FOR UPDATE SKIP LOCKED hands each
// row to exactly one of them.
func TestTwoRelaysPublishEachRowOnce(t *testing.T) {
	db := testutil.DB(t)
	k := testutil.KafkaTopics(t)
	uid := newUser(t, db, testutil.SubPrefix(t, db)+"two-relays")
	const n = 250
	enqueue(t, db, uid, n)

	relays := []*outbox.Relay{
		{DB: db, Client: producer(t, k.Brokers), Topic: k.Topic, BatchSize: 20},
		{DB: db, Client: producer(t, k.Brokers), Topic: k.Topic, BatchSize: 20},
	}
	for unpublished(t, db, uid) > 0 {
		var wg sync.WaitGroup
		errs := make([]error, len(relays))
		for i, r := range relays {
			wg.Go(func() {
				for {
					got, err := r.PublishBatch(context.Background())
					if err != nil || got == 0 {
						errs[i] = err
						return
					}
				}
			})
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	seen := map[string]int{}
	for _, e := range recordsFor(t, k, uid) {
		seen[e.EventID]++
	}
	if len(seen) != n {
		t.Fatalf("published %d distinct events, want %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("event %s published %d times", id, c)
		}
	}
}

// If the broker is down, the produce fails, the transaction rolls back, and rows stay unpublished.
func TestRelayBrokerDownLeavesRowsUnpublished(t *testing.T) {
	db := testutil.DB(t)
	uid := newUser(t, db, testutil.SubPrefix(t, db)+"broker-down")
	enqueue(t, db, uid, 2)

	dead, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:1"), kgo.RecordDeliveryTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer dead.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := (&outbox.Relay{DB: db, Client: dead, Topic: "unused"}).PublishBatch(ctx); err == nil {
		t.Fatal("PublishBatch with no broker succeeded")
	}
	if n := unpublished(t, db, uid); n != 2 {
		t.Fatalf("unpublished rows = %d, want 2", n)
	}
}

// Cleanup deletes only rows published before the cutoff; unpublished rows are never deleted.
func TestCleanupDeletesOnlyOldPublishedRows(t *testing.T) {
	db := testutil.DB(t)
	uid := newUser(t, db, testutil.SubPrefix(t, db)+"cleanup")
	ctx := context.Background()
	if _, err := db.Exec(ctx, `
		INSERT INTO outbox (user_id, type, version, payload, created_at, published_at) VALUES
			($1::uuid, 'old',    1, '{}', now() - interval '9 days',  now() - interval '8 days'),
			($1::uuid, 'recent', 1, '{}', now() - interval '2 days',  now() - interval '1 day'),
			($1::uuid, 'stuck',  1, '{}', now() - interval '30 days', NULL)`, uid); err != nil {
		t.Fatal(err)
	}

	if _, err := (&outbox.Relay{DB: db}).Cleanup(ctx, time.Now().Add(-outbox.DefaultRetention)); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT type FROM outbox WHERE user_id = $1::uuid ORDER BY type`, uid)
	if err != nil {
		t.Fatal(err)
	}
	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 || left[0] != "recent" || left[1] != "stuck" {
		t.Fatalf("rows left = %v, want [recent stuck]", left)
	}
}
