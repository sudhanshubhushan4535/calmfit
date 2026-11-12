package worker_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/worker"
)

func headers(r *kgo.Record) map[string]string {
	h := map[string]string{}
	for _, x := range r.Headers {
		h[x.Key] = string(x.Value)
	}
	return h
}

func (e *env) replayGroup() string { return e.k.DLQ + "-replay" }

// replay runs the DLQ replayer once (in a per-test group) and returns how many records it moved.
func (e *env) replay(t *testing.T, dryRun bool) (int, string) {
	t.Helper()
	cl, err := worker.NewConsumer(e.k.Brokers, e.replayGroup(), e.k.DLQ)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	n, err := (&worker.Replayer{Client: cl, Admin: kadm.NewClient(cl), Group: e.replayGroup(), DLQ: e.k.DLQ, Topic: e.k.Topic,
		DryRun: dryRun, Out: &out}).Run(ctx)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	return n, out.String()
}

// A malformed record goes to the DLQ after one attempt, and the next record on the same partition
// is still processed: one poison message can't block a user's events.
func TestPoisonMessageGoesToDLQWithoutBlocking(t *testing.T) {
	e := setup(t)
	uid := e.user(t, "poison")
	at := time.Date(2026, 11, 11, 18, 0, 0, 0, e.cfg.Location)
	good := workoutEvent(t, uid, at, 2)
	e.produce(t, uid, []byte(`{"event_id": not json`), good)

	stop, _, _ := e.startWorker(t, nil)
	e.waitCommitted(t)
	if err := stop(); err != nil {
		t.Fatalf("worker stopped with %v", err)
	}

	dlq := testutil.ReadAll(t, e.k.Brokers, e.k.DLQ)
	if len(dlq) != 1 {
		t.Fatalf("DLQ has %d records, want 1", len(dlq))
	}
	h := headers(dlq[0])
	if string(dlq[0].Key) != uid || string(dlq[0].Value) != `{"event_id": not json` ||
		h[worker.HeaderAttempts] != "1" || h[worker.HeaderSrcTopic] != e.k.Topic || h[worker.HeaderSrcOffset] != "0" ||
		!strings.Contains(h[worker.HeaderError], "decode envelope") || h[worker.HeaderFailedAt] == "" {
		t.Fatalf("DLQ record key=%s value=%s headers=%v", dlq[0].Key, dlq[0].Value, h)
	}
	if s, _ := e.stats(t, uid); s.workouts != 1 {
		t.Fatalf("the event after the poison message was not applied: %+v", s)
	}
}

// The full story: a transient failure is retried exactly MaxAttempts times, goes to the DLQ, the
// bug is fixed, the DLQ is replayed, and the event is applied once. Re-delivering it changes nothing.
func TestTransientFailureRetriesThenDLQThenReplay(t *testing.T) {
	e := setup(t)
	uid := e.user(t, "flaky")
	at := time.Date(2026, 11, 11, 18, 0, 0, 0, e.cfg.Location)
	ev := workoutEvent(t, uid, at, 3)

	var broken atomic.Bool
	var attempts atomic.Int32
	broken.Store(true)
	opt := func(w *worker.Worker) {
		real := w.Handlers[worker.Key{Type: events.TypeWorkoutLogged, Version: 1}]
		w.Handlers[worker.Key{Type: events.TypeWorkoutLogged, Version: 1}] = func(ctx context.Context, tx pgx.Tx, en events.Envelope) error {
			if broken.Load() {
				attempts.Add(1)
				return errors.New("simulated bug in derived logic")
			}
			return real(ctx, tx, en)
		}
		w.MaxAttempts = 5
		w.BaseBackoff = time.Millisecond
	}

	e.produce(t, uid, ev)
	stop, _, _ := e.startWorker(t, opt)
	e.waitCommitted(t)
	if got := attempts.Load(); got != 5 {
		t.Fatalf("attempts = %d, want exactly 5", got)
	}
	dlq := testutil.ReadAll(t, e.k.Brokers, e.k.DLQ)
	if len(dlq) != 1 || headers(dlq[0])[worker.HeaderAttempts] != "5" {
		t.Fatalf("DLQ = %d records (headers %v), want 1 with attempts=5", len(dlq), headers(dlq[0]))
	}
	if e.processed(t, ev) {
		t.Fatal("a failed event was recorded as processed")
	}
	if _, rows := e.stats(t, uid); rows != 0 {
		t.Fatal("a failed event changed weekly_stats")
	}

	// Dry run lists the record and changes nothing.
	if n, out := e.replay(t, true); n != 1 || !strings.Contains(out, "simulated bug") {
		t.Fatalf("dry run: n=%d out=%q", n, out)
	}
	if n, _ := e.replay(t, true); n != 1 {
		t.Fatalf("second dry run saw %d records, want the same 1", n)
	}

	// Fix the bug, replay, and the running worker applies the event.
	broken.Store(false)
	if n, _ := e.replay(t, false); n != 1 {
		t.Fatalf("replayed %d records, want 1", n)
	}
	e.waitCommitted(t)
	e.waitGroupCommitted(t, e.replayGroup(), e.k.DLQ)
	if s, _ := e.stats(t, uid); s != (stats{1, 3, 3 * 500000}) {
		t.Fatalf("after replay: %+v, want 1 workout, 3 sets", s)
	}
	if n, _ := e.replay(t, false); n != 0 {
		t.Fatalf("second replay moved %d records, want 0", n)
	}

	// The same event arriving yet again (e.g. replayed twice by hand) is a duplicate.
	e.produce(t, uid, ev)
	e.waitCommitted(t)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.stats(t, uid); s.workouts != 1 {
		t.Fatalf("re-delivered event double counted: %+v", s)
	}
}

// Replaying a record that is still poison sends it back to the DLQ (tagged as replayed); nothing
// crashes and nothing loops.
func TestReplayedPoisonReturnsToDLQ(t *testing.T) {
	e := setup(t)
	uid := e.user(t, "repoison")
	e.produce(t, uid, []byte(`not an envelope`))

	stop, _, _ := e.startWorker(t, nil)
	e.waitCommitted(t)
	if n, _ := e.replay(t, false); n != 1 {
		t.Fatalf("replayed %d, want 1", n)
	}
	e.waitCommitted(t)
	if err := stop(); err != nil {
		t.Fatalf("worker stopped with %v", err)
	}

	dlq := testutil.ReadAll(t, e.k.Brokers, e.k.DLQ)
	if len(dlq) != 2 {
		t.Fatalf("DLQ has %d records, want 2 (original + failed replay)", len(dlq))
	}
	if h := headers(dlq[1]); h[worker.HeaderReplayedFrom] != e.k.DLQ+"/0@0" || h[worker.HeaderSrcOffset] != "1" {
		t.Fatalf("second DLQ record headers = %v", h)
	}
	// Only the new DLQ record is pending for the next replay.
	if n, _ := e.replay(t, true); n != 1 {
		t.Fatalf("pending after replay = %d, want 1", n)
	}
}

// If the DLQ produce fails, the worker stops without committing the record, so it isn't lost.
func TestDLQProduceFailureDoesNotCommit(t *testing.T) {
	e := setup(t)
	uid := e.user(t, "nodlq")
	e.produce(t, uid, []byte(`not an envelope`))

	stop, done, runErr := e.startWorker(t, func(w *worker.Worker) { w.DLQTopic = e.k.Topic + "-missing" })
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("worker kept running although the DLQ produce failed")
	}
	_ = stop()
	if *runErr == nil || !strings.Contains((*runErr).Error(), "DLQ produce failed") {
		t.Fatalf("Run error = %v, want a DLQ produce failure", *runErr)
	}

	cl, err := kgo.NewClient(kgo.SeedBrokers(e.k.Brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	committed, err := kadm.NewClient(cl).FetchOffsets(context.Background(), e.group)
	if err != nil {
		t.Fatal(err)
	}
	committed.Each(func(o kadm.OffsetResponse) {
		if o.Topic == e.k.Topic && o.At > 0 {
			t.Errorf("partition %d committed at %d: the record would be lost", o.Partition, o.At)
		}
	})
}
