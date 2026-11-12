// Command drill runs the steps of the failure drills (scripts/drills/*.sh, docs/DRILLS.md).
//
//	drill members                       exit 1 if the calmfit-stats group has members
//	drill users -run R -n N             create N drill users
//	drill enqueue -run R -events E -rate S -out F   enqueue workout.logged events, write expected totals
//	drill wait-idle -timeout D          wait until the outbox is drained and the group lag is 0
//	drill verify -run R -expect F       compare weekly_stats/processed_events with the expected totals
//	drill duplicate -run R -republish K -times T   re-publish via the outbox + produce one event T more times
//	drill malformed -run R              produce bad records, then a valid one with the same key
//	drill snapshot -out F | drill compare A B
//	drill redis -phase up|down          in-process: voice must fail closed, the rest must not care
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	mrand "math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/drill"
	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/worker"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if len(os.Args) < 2 {
		log.Fatal("usage: drill <members|users|enqueue|wait-idle|verify|duplicate|malformed|snapshot|compare|redis> [flags]")
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "members":
		err = members()
	case "users":
		err = users(args)
	case "enqueue":
		err = enqueue(args)
	case "wait-idle":
		err = waitIdle(args)
	case "verify":
		err = verify(args)
	case "duplicate":
		err = duplicate(args)
	case "malformed":
		err = malformed(args)
	case "snapshot":
		err = snapshot(args)
	case "compare":
		err = compare(args)
	case "redis":
		err = redisDrill(args)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Fatalf("drill %s: %v", cmd, err)
	}
}

func env(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is not set (see .env.example)", key)
	}
	return v
}

func db(ctx context.Context) *pgxpool.Pool {
	p, err := pgxpool.New(ctx, env("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	return p
}

func kafka(opts ...kgo.Opt) (*kgo.Client, *kadm.Client) {
	cl, err := kgo.NewClient(append([]kgo.Opt{kgo.SeedBrokers(strings.Split(env("KAFKA_BROKERS"), ",")...)}, opts...)...)
	if err != nil {
		log.Fatal(err)
	}
	return cl, kadm.NewClient(cl)
}

func appConfig() config.Config {
	path := os.Getenv("APP_CONFIG")
	if path == "" {
		path = "config/app.json"
	}
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatal(err)
	}
	return cfg
}

func members() error {
	cl, adm := kafka()
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gs, err := adm.DescribeGroups(ctx, worker.Group)
	if err != nil {
		return err
	}
	n := 0
	for _, g := range gs {
		n += len(g.Members)
	}
	fmt.Printf("group %s: %d member(s)\n", worker.Group, n)
	if n > 0 {
		return fmt.Errorf("stop the running workers first (the drill starts its own two)")
	}
	return nil
}

func users(args []string) error {
	fs := flag.NewFlagSet("users", flag.ExitOnError)
	run := fs.String("run", "", "run id")
	n := fs.Int("n", 50, "users")
	_ = fs.Parse(args)
	ctx := context.Background()
	p := db(ctx)
	defer p.Close()
	ids, err := drill.CreateUsers(ctx, p, *run, *n)
	if err == nil {
		fmt.Printf("created %d drill users drill-%s-NNN\n", len(ids), *run)
	}
	return err
}

func enqueue(args []string) error {
	fs := flag.NewFlagSet("enqueue", flag.ExitOnError)
	run := fs.String("run", "", "run id")
	n := fs.Int("events", 1000, "events to enqueue")
	rate := fs.Int("rate", 100, "events per second")
	seed := fs.Uint64("seed", 1, "rng seed for sets")
	out := fs.String("out", "", "write the expected totals here")
	_ = fs.Parse(args)
	ctx := context.Background()
	p := db(ctx)
	defer p.Close()
	ids, err := drill.Users(ctx, p, *run)
	if err != nil || len(ids) == 0 {
		return fmt.Errorf("no drill users for run %q: %v", *run, err)
	}
	start := time.Now()
	t, err := drill.Enqueue(ctx, p, ids, *n, *rate, mrand.New(mrand.NewPCG(*seed, 0)))
	if err != nil {
		return err
	}
	log.Printf("enqueued %d events in %s (%.0f/s): %d sets, %d g volume", t.Events, time.Since(start).Round(time.Millisecond),
		float64(t.Events)/time.Since(start).Seconds(), t.Sets, t.VolumeG)
	if *out != "" {
		return drill.WriteJSON(*out, t)
	}
	return nil
}

// idle reports the unpublished outbox rows and the group's total lag.
func idle(ctx context.Context, p *pgxpool.Pool, adm *kadm.Client) (int64, int64, error) {
	var unpublished int64
	if err := p.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&unpublished); err != nil {
		return 0, 0, err
	}
	lags, _, err := worker.NewKafkaCollector(adm, worker.Group, events.Topic, events.DLQTopic).Read(ctx)
	var lag int64
	for _, l := range lags {
		lag += l
	}
	return unpublished, lag, err
}

func waitIdle(args []string) error {
	fs := flag.NewFlagSet("wait-idle", flag.ExitOnError)
	timeout := fs.Duration("timeout", 3*time.Minute, "give up after")
	_ = fs.Parse(args)
	ctx := context.Background()
	p := db(ctx)
	defer p.Close()
	cl, adm := kafka()
	defer cl.Close()
	start := time.Now()
	for {
		u, lag, err := idle(ctx, p, adm)
		if err == nil && u == 0 && lag == 0 {
			log.Printf("idle after %s: outbox drained, lag 0", time.Since(start).Round(time.Millisecond))
			return nil
		}
		if time.Since(start) > *timeout {
			return fmt.Errorf("not idle after %s: %d unpublished, lag %d (err %v)", *timeout, u, lag, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	run := fs.String("run", "", "run id")
	expect := fs.String("expect", "", "expected totals JSON (from enqueue -out)")
	_ = fs.Parse(args)
	var want drill.Totals
	if err := drill.ReadJSON(*expect, &want); err != nil {
		return err
	}
	ctx := context.Background()
	p := db(ctx)
	defer p.Close()
	got, err := drill.Observe(ctx, p, *run)
	if err != nil {
		return err
	}
	fmt.Printf("events enqueued %d, in processed_events %d\n", got.Enqueued, got.Processed)
	fmt.Printf("weekly_stats  expected workouts=%d sets=%d volume_g=%d\n", want.Workouts, want.Sets, want.VolumeG)
	fmt.Printf("weekly_stats  observed workouts=%d sets=%d volume_g=%d\n", got.Workouts, got.Sets, got.VolumeG)
	if got.Enqueued != want.Events || got.Processed != want.Events || got.Totals != want {
		fmt.Println("RESULT: FAIL")
		return errors.New("mismatch")
	}
	fmt.Println("RESULT: PASS (nothing lost, nothing counted twice)")
	return nil
}

// duplicate forces two kinds of duplicate delivery and checks nothing is counted twice:
// K already-published outbox rows are marked unpublished, so the real relay publishes them again;
// and one event is produced T more times straight to the topic.
func duplicate(args []string) error {
	fs := flag.NewFlagSet("duplicate", flag.ExitOnError)
	run := fs.String("run", "", "run id")
	k := fs.Int("republish", 100, "published outbox rows to publish again")
	times := fs.Int("times", 3, "extra copies of one event produced directly")
	_ = fs.Parse(args)
	ctx := context.Background()
	p := db(ctx)
	defer p.Close()
	cl, _ := kafka()
	defer cl.Close()

	before, err := drill.Observe(ctx, p, *run)
	if err != nil {
		return err
	}
	tag, err := p.Exec(ctx, `
		UPDATE outbox SET published_at = NULL WHERE id IN (
			SELECT o.id FROM outbox o JOIN users u ON u.id = o.user_id
			WHERE u.google_sub LIKE $1 AND o.published_at IS NOT NULL ORDER BY o.id LIMIT $2)`, drill.SubLike(*run), *k)
	if err != nil {
		return err
	}
	var e events.Envelope
	if err := p.QueryRow(ctx, `
		SELECT o.event_id::text, o.user_id::text, o.type, o.version, o.payload, o.created_at
		FROM outbox o JOIN users u ON u.id = o.user_id WHERE u.google_sub LIKE $1 ORDER BY o.id DESC LIMIT 1`,
		drill.SubLike(*run)).Scan(&e.EventID, &e.UserID, &e.Type, &e.Version, &e.Data, &e.OccurredAt); err != nil {
		return err
	}
	value, err := json.Marshal(e) // byte for byte what the relay sends
	if err != nil {
		return err
	}
	var recs []*kgo.Record
	for range *times {
		recs = append(recs, &kgo.Record{Topic: events.Topic, Key: []byte(e.UserID), Value: value})
	}
	if err := cl.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		return err
	}
	log.Printf("before: weekly_stats workouts=%d sets=%d volume_g=%d", before.Workouts, before.Sets, before.VolumeG)
	log.Printf("re-queued %d published outbox rows for the relay; produced event %s %d more times directly", tag.RowsAffected(), e.EventID, *times)
	return nil
}

// malformed produces a non-JSON record and an envelope with an unknown version, keyed like a real
// user's events, then enqueues one valid event for the same user through the outbox. The two bad
// records must land in the DLQ and the valid one behind them must still be applied.
func malformed(args []string) error {
	fs := flag.NewFlagSet("malformed", flag.ExitOnError)
	run := fs.String("run", "", "run id")
	timeout := fs.Duration("timeout", 60*time.Second, "give up after")
	_ = fs.Parse(args)
	ctx := context.Background()
	p := db(ctx)
	defer p.Close()
	cl, adm := kafka()
	defer cl.Close()

	ids, err := drill.Users(ctx, p, *run)
	if err != nil || len(ids) == 0 {
		return fmt.Errorf("no drill users for run %q: %v", *run, err)
	}
	user := ids[0]
	dlqBefore, err := dlqEnd(ctx, adm)
	if err != nil {
		return err
	}
	bad := events.Envelope{EventID: "00000000-0000-4000-8000-0000000000aa", Type: events.TypeWorkoutLogged, Version: 99,
		UserID: user, OccurredAt: time.Now().UTC(), Data: json.RawMessage(`{}`)}
	badV, _ := json.Marshal(bad)
	recs := []*kgo.Record{
		{Topic: events.Topic, Key: []byte(user), Value: []byte(`{"event_id": "not json at all`)},
		{Topic: events.Topic, Key: []byte(user), Value: badV},
	}
	res := cl.ProduceSync(ctx, recs...)
	if err := res.FirstErr(); err != nil {
		return err
	}
	for _, r := range res {
		log.Printf("produced bad record to %s/%d@%d", r.Record.Topic, r.Record.Partition, r.Record.Offset)
	}
	before, err := drill.Observe(ctx, p, *run)
	if err != nil {
		return err
	}
	sets, err := drill.EnqueueOne(ctx, p, user, mrand.New(mrand.NewPCG(99, 0)))
	if err != nil {
		return err
	}
	var want drill.Totals
	want.Add(sets)
	log.Printf("enqueued one valid event for the same user (key) through the outbox")

	start := time.Now()
	for {
		after, err := drill.Observe(ctx, p, *run)
		if err != nil {
			return err
		}
		dlqNow, err := dlqEnd(ctx, adm)
		if err != nil {
			return err
		}
		if dlqNow-dlqBefore >= 2 && after.Processed == before.Processed+1 {
			ok := after.Workouts-before.Workouts == want.Workouts && after.Sets-before.Sets == want.Sets && after.VolumeG-before.VolumeG == want.VolumeG
			fmt.Printf("DLQ records: %d → %d (+%d)\n", dlqBefore, dlqNow, dlqNow-dlqBefore)
			fmt.Printf("valid event behind them applied after %s: +%d workout, +%d sets, +%d g\n",
				time.Since(start).Round(time.Millisecond), after.Workouts-before.Workouts, after.Sets-before.Sets, after.VolumeG-before.VolumeG)
			if err := printDLQ(ctx, dlqBefore, dlqNow); err != nil {
				return err
			}
			if !ok || dlqNow-dlqBefore != 2 {
				fmt.Println("RESULT: FAIL")
				return errors.New("unexpected counts")
			}
			fmt.Println("RESULT: PASS (both bad records in the DLQ, partition not blocked)")
			return nil
		}
		if time.Since(start) > *timeout {
			return fmt.Errorf("timeout: DLQ +%d, valid event processed: %v", dlqNow-dlqBefore, after.Processed > before.Processed)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func dlqEnd(ctx context.Context, adm *kadm.Client) (int64, error) {
	ends, err := adm.ListEndOffsets(ctx, events.DLQTopic)
	if err != nil {
		return 0, err
	}
	var n int64
	ends.Each(func(o kadm.ListedOffset) { n += o.Offset })
	return n, ends.Error()
}

// printDLQ prints the DLQ records in [from, to) of partition 0 (the DLQ has one partition).
func printDLQ(ctx context.Context, from, to int64) error {
	cl, _ := kafka(kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{events.DLQTopic: {0: kgo.NewOffset().At(from)}}))
	defer cl.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for seen := from; seen < to; {
		fs := cl.PollFetches(ctx)
		if err := fs.Err0(); err != nil {
			return err
		}
		for _, r := range fs.Records() {
			h := map[string]string{}
			for _, x := range r.Headers {
				h[x.Key] = string(x.Value)
			}
			fmt.Printf("  DLQ @%d from %s/%s@%s attempts=%s error=%q\n", r.Offset, h[worker.HeaderSrcTopic],
				h[worker.HeaderSrcPartition], h[worker.HeaderSrcOffset], h[worker.HeaderAttempts], h[worker.HeaderError])
			seen = r.Offset + 1
		}
	}
	return nil
}

func snapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	out := fs.String("out", "", "write the snapshot here")
	_ = fs.Parse(args)
	ctx := context.Background()
	p := db(ctx)
	defer p.Close()
	s, err := drill.TakeSnapshot(ctx, p)
	if err != nil {
		return err
	}
	fmt.Printf("weekly_stats: %d rows, workouts=%d sets=%d volume_g=%d md5=%s\n", s.Rows, s.Workouts, s.Sets, s.VolumeG, s.MD5)
	return drill.WriteJSON(*out, s)
}

func compare(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: drill compare BEFORE.json AFTER.json")
	}
	var a, b drill.Snapshot
	if err := drill.ReadJSON(args[0], &a); err != nil {
		return err
	}
	if err := drill.ReadJSON(args[1], &b); err != nil {
		return err
	}
	fmt.Printf("before: %+v\nafter:  %+v\n", a, b)
	if a != b {
		fmt.Println("RESULT: FAIL (weekly_stats differ)")
		return errors.New("mismatch")
	}
	fmt.Println("RESULT: PASS (weekly_stats identical: rows, sums and md5)")
	return nil
}
