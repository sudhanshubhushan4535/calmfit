package worker_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/outbox"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/worker"
)

type env struct {
	db    *pgxpool.Pool
	k     testutil.Kafka
	cfg   config.Config
	group string
	sub   string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.DB(t)
	k := testutil.KafkaTopics(t)
	return &env{db: db, k: k, cfg: testutil.Config(t), group: k.Topic + "-group", sub: testutil.SubPrefix(t, db)}
}

func (e *env) user(t *testing.T, name string) string {
	t.Helper()
	var id string
	if err := e.db.QueryRow(context.Background(),
		`INSERT INTO users (google_sub, email) VALUES ($1, $1 || '@example.com') RETURNING id::text`, e.sub+name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// workoutEvent builds a workout.logged envelope completed at `at` with sets of 50 kg × 10.
func workoutEvent(t *testing.T, uid string, at time.Time, sets int) []byte {
	t.Helper()
	ev := events.WorkoutLogged{WorkoutID: uuid(), Day: "push", CompletedAt: at}
	for i := range sets {
		ev.Sets = append(ev.Sets, events.LoggedSet{ExerciseID: "Barbell_Squat", SetNumber: i%3 + 1, WeightG: 50000, Reps: 10})
	}
	return envelope(t, uid, events.TypeWorkoutLogged, ev)
}

func envelope(t *testing.T, uid, typ string, data any) []byte {
	t.Helper()
	d, _ := json.Marshal(data)
	b, err := json.Marshal(events.Envelope{EventID: uuid(), Type: typ, Version: 1, UserID: uid, OccurredAt: time.Now(), Data: d})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) produce(t *testing.T, uid string, values ...[]byte) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(e.k.Brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	for _, v := range values {
		if err := cl.ProduceSync(context.Background(), &kgo.Record{Topic: e.k.Topic, Key: []byte(uid), Value: v}).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
}

// startWorker runs a worker in the test group. done is closed when Run returns; stop cancels it,
// closes the client (leaving the group) and returns Run's error. Both are safe to use together.
func (e *env) startWorker(t *testing.T, opt func(*worker.Worker)) (stop func() error, done <-chan struct{}, runErr *error) {
	t.Helper()
	cl, err := worker.NewConsumer(e.k.Brokers, e.group, e.k.Topic)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan struct{})
	var rerr error
	w := &worker.Worker{DB: e.db, Client: cl, Handlers: worker.DefaultHandlers(e.cfg), DLQTopic: e.k.DLQ}
	if opt != nil {
		opt(w)
	}
	go func() { rerr = w.Run(ctx); close(ch) }()
	var once sync.Once
	stop = func() error {
		once.Do(func() {
			cancel()
			<-ch
			cl.Close()
		})
		return rerr
	}
	t.Cleanup(func() { _ = stop() })
	return stop, ch, &rerr
}

// waitCommitted waits until the worker group has committed every record currently in the topic.
func (e *env) waitCommitted(t *testing.T) { e.waitGroupCommitted(t, e.group, e.k.Topic) }

// waitGroupCommitted waits until group has committed every record currently in topic.
func (e *env) waitGroupCommitted(t *testing.T, group, topic string) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(e.k.Brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ctx := context.Background()
	ends, err := adm.ListEndOffsets(ctx, topic)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		committed, err := adm.FetchOffsets(ctx, group)
		if err == nil {
			behind := 0
			ends.Each(func(o kadm.ListedOffset) {
				if o.Offset == 0 {
					return // empty partition: nothing to commit
				}
				if c, ok := committed.Lookup(o.Topic, o.Partition); !ok || c.At < o.Offset {
					behind++
				}
			})
			if behind == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("group %s did not commit to the end of %s (last err %v)", group, topic, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type stats struct {
	workouts, sets int
	volume         int64
}

func (e *env) stats(t *testing.T, uid string) (stats, int) {
	t.Helper()
	var s stats
	var rows int
	if err := e.db.QueryRow(context.Background(), `
		SELECT coalesce(sum(workouts), 0), coalesce(sum(sets), 0), coalesce(sum(volume_g), 0), count(*)
		FROM weekly_stats WHERE user_id = $1::uuid`, uid).Scan(&s.workouts, &s.sets, &s.volume, &rows); err != nil {
		t.Fatal(err)
	}
	return s, rows
}

func (e *env) processed(t *testing.T, value []byte) bool {
	t.Helper()
	var env events.Envelope
	_ = json.Unmarshal(value, &env)
	var ok bool
	if err := e.db.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM processed_events WHERE event_id = $1::uuid)`, env.EventID).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// Duplicate delivery never double counts: the same event delivered twice, and an event redelivered
// after a crash between the DB commit and the offset commit, are each applied once.
func TestDuplicateDeliveryIsAppliedOnce(t *testing.T) {
	e := setup(t)
	uid := e.user(t, "dup")
	at := time.Date(2026, 11, 11, 18, 0, 0, 0, e.cfg.Location) // a Wednesday

	// 1. The relay re-published an event: two identical records.
	first := workoutEvent(t, uid, at, 3)
	e.produce(t, uid, first, first)
	stop, _, _ := e.startWorker(t, nil)
	e.waitCommitted(t)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.stats(t, uid); s != (stats{1, 3, 3 * 500000}) {
		t.Fatalf("after duplicate records: %+v, want 1 workout, 3 sets", s)
	}

	// 2. A worker crashes after the DB commit but before committing the offset.
	second := workoutEvent(t, uid, at.Add(time.Hour), 2)
	e.produce(t, uid, second)
	crash := errors.New("simulated crash before offset commit")
	stop, done, runErr := e.startWorker(t, func(w *worker.Worker) {
		w.AfterDBCommit = func(*kgo.Record) error { return crash }
	})
	select {
	case <-done:
		if !errors.Is(*runErr, crash) {
			t.Fatalf("crashing worker returned %v", *runErr)
		}
		_ = stop() // leave the group so the next instance gets the partitions
	case <-time.After(20 * time.Second):
		t.Fatal("crashing worker never processed the record")
	}
	if !e.processed(t, second) {
		t.Fatal("the crashed worker's DB transaction did not commit")
	}

	// A new instance in the same group gets the record again and skips it as a duplicate.
	stop, _, _ = e.startWorker(t, nil)
	e.waitCommitted(t)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	s, rows := e.stats(t, uid)
	if s != (stats{2, 5, 5 * 500000}) || rows != 1 {
		t.Fatalf("after redelivery: %+v in %d weeks, want 2 workouts, 5 sets in 1 week", s, rows)
	}
	var n int
	if err := e.db.QueryRow(context.Background(), `SELECT count(*) FROM processed_events WHERE event_id = ANY($1::uuid[])`,
		[]string{eventID(first), eventID(second)}).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("processed_events rows = %d, want 2", n)
	}
}

func eventID(value []byte) string {
	var env events.Envelope
	_ = json.Unmarshal(value, &env)
	return env.EventID
}

// Events for deleted or unknown users are recorded as processed and skipped; the worker keeps
// going, and user.deleted purges the user's derived rows.
func TestDeletedAndUnknownUsersAreSkipped(t *testing.T) {
	e := setup(t)
	live, gone := e.user(t, "live"), e.user(t, "gone")
	at := time.Date(2026, 11, 11, 18, 0, 0, 0, e.cfg.Location)
	ctx := context.Background()
	if _, err := e.db.Exec(ctx, `
		INSERT INTO weekly_stats (user_id, week_start, workouts, sets, volume_g) VALUES ($1::uuid, '2026-11-09', 4, 12, 1)`, gone); err != nil {
		t.Fatal(err)
	}
	// A tombstone as account deletion leaves it (D29); SubPrefix can't find it, so clean up by id.
	if _, err := e.db.Exec(ctx, `
		UPDATE users SET deleted_at = now(), google_sub = NULL, email = '', name = NULL WHERE id = $1::uuid`, gone); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.db.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, gone) })

	unknown := uuid()
	late := workoutEvent(t, gone, at, 3)
	stranger := workoutEvent(t, unknown, at, 3)
	purge := envelope(t, gone, events.TypeUserDeleted, events.UserDeleted{})
	ok := workoutEvent(t, live, at, 1)
	e.produce(t, gone, late, purge)
	e.produce(t, unknown, stranger)
	e.produce(t, live, ok)

	stop, _, _ := e.startWorker(t, nil)
	e.waitCommitted(t)
	if err := stop(); err != nil {
		t.Fatalf("worker stopped with %v, want it to keep running", err)
	}
	for name, v := range map[string][]byte{"late": late, "stranger": stranger, "purge": purge, "ok": ok} {
		if !e.processed(t, v) {
			t.Errorf("%s event not recorded as processed", name)
		}
	}
	if _, rows := e.stats(t, gone); rows != 0 {
		t.Errorf("deleted user has %d weekly_stats rows, want 0", rows)
	}
	if _, rows := e.stats(t, unknown); rows != 0 {
		t.Errorf("unknown user has %d weekly_stats rows", rows)
	}
	if s, _ := e.stats(t, live); s.workouts != 1 {
		t.Errorf("live user stats %+v, want 1 workout", s)
	}
}

// End to end: complete a workout over HTTP → outbox → relay → Kafka → worker → weekly_stats.
func TestCompletedWorkoutReachesWeeklyStats(t *testing.T) {
	e := setup(t)
	fv := testutil.NewFakeVerifier()
	srv := httptest.NewServer(server.New(e.db, fv, "test-client-id", e.cfg))
	t.Cleanup(srv.Close)
	c := testutil.Login(t, srv.URL, fv, e.sub+"e2e")
	steps := []struct{ method, path, body string }{
		{"POST", "/onboarding", `{"days_per_week":3}`},
		{"POST", "/workouts/start", ""},
	}
	var wid string
	for _, s := range steps {
		b := testutil.Do(t, c, s.method, srv.URL+s.path, s.body)
		if s.path == "/workouts/start" {
			var w struct{ ID string }
			_ = json.Unmarshal(b, &w)
			wid = w.ID
		}
	}
	testutil.Do(t, c, "POST", srv.URL+"/workouts/"+wid+"/exercises/Side_Lateral_Raise/cold-start", `{"usual_kg":5}`)
	testutil.Do(t, c, "POST", srv.URL+"/workouts/"+wid+"/sets", `{"exercise_id":"Side_Lateral_Raise","set_number":1,"weight_g":5000,"reps":12}`)
	testutil.Do(t, c, "POST", srv.URL+"/workouts/"+wid+"/sets", `{"exercise_id":"Side_Lateral_Raise","set_number":2,"weight_g":7500,"reps":10}`)
	testutil.Do(t, c, "POST", srv.URL+"/workouts/"+wid+"/complete", "")

	var uid string
	if err := e.db.QueryRow(context.Background(), `SELECT id::text FROM users WHERE google_sub = $1`, e.sub+"e2e").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	prod, err := outbox.NewProducer(e.k.Brokers)
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()
	relay := &outbox.Relay{DB: e.db, Client: prod, Topic: e.k.Topic}
	for {
		var left int
		if err := e.db.QueryRow(context.Background(),
			`SELECT count(*) FROM outbox WHERE user_id = $1::uuid AND published_at IS NULL`, uid).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			break
		}
		if _, err := relay.PublishBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	stop, _, _ := e.startWorker(t, nil)
	e.waitCommitted(t)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	s, rows := e.stats(t, uid)
	if s != (stats{1, 2, 5000*12 + 7500*10}) || rows != 1 {
		t.Fatalf("weekly_stats %+v in %d rows, want 1 workout, 2 sets, 135000 volume", s, rows)
	}
}

// Account deletion end to end: stats exist → DELETE /me removes them in the same transaction →
// the user.deleted event is published and applied → a stale workout.logged for the deleted user
// (e.g. a late redelivery or a full replay) is skipped, and nothing is rebuilt.
func TestAccountDeletionEndToEnd(t *testing.T) {
	e := setup(t)
	fv := testutil.NewFakeVerifier()
	srv := httptest.NewServer(server.New(e.db, fv, "test-client-id", e.cfg))
	t.Cleanup(srv.Close)
	c := testutil.Login(t, srv.URL, fv, e.sub+"bye")
	var uid string
	if err := e.db.QueryRow(context.Background(), `SELECT id::text FROM users WHERE google_sub = $1`, e.sub+"bye").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // the tombstone has no google_sub, so SubPrefix can't clean it up
		_, _ = e.db.Exec(context.Background(), `DELETE FROM outbox WHERE user_id = $1::uuid`, uid)
		_, _ = e.db.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, uid)
	})

	testutil.Do(t, c, "POST", srv.URL+"/onboarding", `{"days_per_week":3}`)
	var w struct{ ID string }
	_ = json.Unmarshal(testutil.Do(t, c, "POST", srv.URL+"/workouts/start", ""), &w)
	testutil.Do(t, c, "POST", srv.URL+"/workouts/"+w.ID+"/exercises/Side_Lateral_Raise/cold-start", `{"usual_kg":5}`)
	testutil.Do(t, c, "POST", srv.URL+"/workouts/"+w.ID+"/sets", `{"exercise_id":"Side_Lateral_Raise","set_number":1,"weight_g":5000,"reps":12}`)
	testutil.Do(t, c, "POST", srv.URL+"/workouts/"+w.ID+"/complete", "")

	prod, err := outbox.NewProducer(e.k.Brokers)
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()
	relay := &outbox.Relay{DB: e.db, Client: prod, Topic: e.k.Topic}
	publish := func() {
		for {
			var left int
			if err := e.db.QueryRow(context.Background(),
				`SELECT count(*) FROM outbox WHERE user_id = $1::uuid AND published_at IS NULL`, uid).Scan(&left); err != nil {
				t.Fatal(err)
			}
			if left == 0 {
				return
			}
			if _, err := relay.PublishBatch(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	publish()
	stop, _, _ := e.startWorker(t, nil)
	e.waitCommitted(t)
	if s, _ := e.stats(t, uid); s.workouts != 1 {
		t.Fatalf("before delete: %+v", s)
	}

	testutil.Do(t, c, "DELETE", srv.URL+"/me", "")
	if _, rows := e.stats(t, uid); rows != 0 {
		t.Fatal("deletion left weekly_stats rows")
	}
	publish() // user.deleted
	stale := workoutEvent(t, uid, time.Now(), 3)
	e.produce(t, uid, stale)
	e.waitCommitted(t)
	if err := stop(); err != nil {
		t.Fatalf("worker failed on a deleted user's events: %v", err)
	}
	if _, rows := e.stats(t, uid); rows != 0 {
		t.Fatal("a stale event rebuilt stats for a deleted user")
	}
	if !e.processed(t, stale) {
		t.Fatal("the stale event was not recorded as processed")
	}
	if dlq := testutil.ReadAll(t, e.k.Brokers, e.k.DLQ); len(dlq) != 0 {
		t.Fatalf("deleted user's events went to the DLQ: %d records", len(dlq))
	}
}
