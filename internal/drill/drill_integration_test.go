package drill

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/worker"
)

// The drill's expected totals must follow the worker's own rule: enqueue events, apply them with
// the real worker code, and compare.
func TestObserveMatchesWorker(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	run := "test" + testutil.SubPrefix(t, db)[5:17] // unique; the users are drill-<run>-NNN
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM processed_events WHERE event_id IN (
			SELECT o.event_id FROM outbox o JOIN users u ON u.id = o.user_id WHERE u.google_sub LIKE $1)`, SubLike(run))
		_, _ = db.Exec(ctx, `DELETE FROM outbox WHERE user_id IN (SELECT id FROM users WHERE google_sub LIKE $1)`, SubLike(run))
		_, _ = db.Exec(ctx, `DELETE FROM users WHERE google_sub LIKE $1`, SubLike(run))
	})

	ids, err := CreateUsers(ctx, db, run, 3)
	if err != nil {
		t.Fatal(err)
	}
	before, err := TakeSnapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Enqueue(ctx, db, ids, 6, 1000, rand.New(rand.NewPCG(1, 1)))
	if err != nil {
		t.Fatal(err)
	}
	o, err := Observe(ctx, db, run)
	if err != nil {
		t.Fatal(err)
	}
	if o.Enqueued != 6 || o.Processed != 0 || o.Workouts != 0 {
		t.Fatalf("before applying: %+v", o)
	}

	// Apply each outbox row exactly as the relay would publish it, with the real worker.
	w := &worker.Worker{DB: db, Handlers: worker.DefaultHandlers(testutil.Config(t))}
	rows, err := db.Query(ctx, `
		SELECT o.event_id::text, o.user_id::text, o.type, o.version, o.payload, o.created_at
		FROM outbox o JOIN users u ON u.id = o.user_id WHERE u.google_sub LIKE $1 ORDER BY o.id`, SubLike(run))
	if err != nil {
		t.Fatal(err)
	}
	var vals [][]byte
	for rows.Next() {
		var e events.Envelope
		if err := rows.Scan(&e.EventID, &e.UserID, &e.Type, &e.Version, &e.Data, &e.OccurredAt); err != nil {
			t.Fatal(err)
		}
		v, _ := json.Marshal(e)
		vals = append(vals, v)
	}
	for _, v := range append(vals, vals[0]) { // plus one duplicate
		if _, _, err := w.Apply(ctx, v); err != nil {
			t.Fatal(err)
		}
	}

	o, err = Observe(ctx, db, run)
	if err != nil {
		t.Fatal(err)
	}
	if o.Enqueued != 6 || o.Processed != 6 || o.Totals != want {
		t.Fatalf("observed %+v, want totals %+v and 6 processed", o, want)
	}
	after, err := TakeSnapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if after == before || after.Rows < 1 {
		t.Fatalf("snapshot didn't change: %+v", after)
	}
}
