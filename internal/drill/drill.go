// Package drill holds the building blocks of the failure drills (docs/DRILLS.md, make drill-*).
// Drill events belong to dedicated users ("drill-<run>-NNN") and go through outbox.Enqueue in
// real transactions, the same path as the API, so what each drill should produce is known exactly.
package drill

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/outbox"
)

// SubLike matches every drill user of a run.
func SubLike(run string) string { return "drill-" + run + "-%" }

// Totals is what a set of workout.logged events adds to weekly_stats, summed over all weeks.
type Totals struct {
	Events   int64 `json:"events"`
	Workouts int64 `json:"workouts"`
	Sets     int64 `json:"sets"`
	VolumeG  int64 `json:"volume_g"`
}

// Add accounts for one workout.logged event with these sets (the worker's own rule: +1 workout,
// +len(sets), +Σ weight×reps).
func (t *Totals) Add(sets []events.LoggedSet) {
	t.Events++
	t.Workouts++
	t.Sets += int64(len(sets))
	for _, s := range sets {
		t.VolumeG += int64(s.WeightG) * int64(s.Reps)
	}
}

// RandomSets returns 5 exercises × 3 sets with plausible weights and reps.
func RandomSets(rng *mrand.Rand) []events.LoggedSet {
	exs := []string{"Incline_Dumbbell_Press", "Butterfly", "Dumbbell_Shoulder_Press", "Side_Lateral_Raise", "Triceps_Pushdown"}
	var out []events.LoggedSet
	for _, ex := range exs {
		w := 2500 * (4 + rng.IntN(20))
		for n := 1; n <= 3; n++ {
			out = append(out, events.LoggedSet{ExerciseID: ex, SetNumber: n, WeightG: w + 2500*(n-1), Reps: 6 + rng.IntN(8)})
		}
	}
	return out
}

// CreateUsers inserts n drill users for run and returns their ids.
func CreateUsers(ctx context.Context, db *pgxpool.Pool, run string, n int) ([]string, error) {
	var ids []string
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for i := range n {
			sub := fmt.Sprintf("drill-%s-%03d", run, i)
			var id string
			if err := tx.QueryRow(ctx, `INSERT INTO users (google_sub, email, name) VALUES ($1, $1 || '@drill.invalid', 'Drill user')
				RETURNING id::text`, sub).Scan(&id); err != nil {
				return fmt.Errorf("create %s: %w", sub, err)
			}
			ids = append(ids, id)
		}
		return nil
	})
	return ids, err
}

// Users returns the ids of run's drill users.
func Users(ctx context.Context, db *pgxpool.Pool, run string) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT id::text FROM users WHERE google_sub LIKE $1 ORDER BY google_sub`, SubLike(run))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// EnqueueOne writes one workout.logged event for user in its own transaction, as the API's
// complete does, and returns its sets.
func EnqueueOne(ctx context.Context, db *pgxpool.Pool, user string, rng *mrand.Rand) ([]events.LoggedSet, error) {
	sets := RandomSets(rng)
	ev := events.WorkoutLogged{WorkoutID: newUUID(), Day: "push", CompletedAt: time.Now().UTC(), Sets: sets}
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		return outbox.Enqueue(ctx, tx, user, events.TypeWorkoutLogged, events.V1, ev)
	})
	return sets, err
}

// Enqueue writes n events spread round-robin over users at about rate events/s and returns the
// expected totals.
func Enqueue(ctx context.Context, db *pgxpool.Pool, users []string, n, rate int, rng *mrand.Rand) (Totals, error) {
	var t Totals
	tick := time.NewTicker(time.Second / time.Duration(rate))
	defer tick.Stop()
	for i := range n {
		<-tick.C
		sets, err := EnqueueOne(ctx, db, users[i%len(users)], rng)
		if err != nil {
			return t, err
		}
		t.Add(sets)
	}
	return t, nil
}

// Observed is the run's events as seen by the worker: the weekly_stats sums of its users, and how
// many of its outbox events are in processed_events.
type Observed struct {
	Totals
	Enqueued  int64 `json:"enqueued"`
	Processed int64 `json:"processed"`
}

// Observe reads the run's current state.
func Observe(ctx context.Context, db *pgxpool.Pool, run string) (Observed, error) {
	var o Observed
	err := db.QueryRow(ctx, `
		SELECT coalesce(sum(ws.workouts), 0), coalesce(sum(ws.sets), 0), coalesce(sum(ws.volume_g), 0)
		FROM weekly_stats ws JOIN users u ON u.id = ws.user_id WHERE u.google_sub LIKE $1`, SubLike(run)).
		Scan(&o.Workouts, &o.Sets, &o.VolumeG)
	if err != nil {
		return o, err
	}
	o.Events = o.Workouts
	err = db.QueryRow(ctx, `
		SELECT count(*), count(p.event_id)
		FROM outbox o JOIN users u ON u.id = o.user_id
		LEFT JOIN processed_events p ON p.event_id = o.event_id
		WHERE u.google_sub LIKE $1 AND o.type = $2`, SubLike(run), events.TypeWorkoutLogged).
		Scan(&o.Enqueued, &o.Processed)
	return o, err
}

// Snapshot fingerprints weekly_stats: row count, sums and an md5 over every row in key order.
type Snapshot struct {
	Rows     int64  `json:"rows"`
	Workouts int64  `json:"workouts"`
	Sets     int64  `json:"sets"`
	VolumeG  int64  `json:"volume_g"`
	MD5      string `json:"md5"`
}

// TakeSnapshot reads weekly_stats in one statement (one consistent view).
func TakeSnapshot(ctx context.Context, db *pgxpool.Pool) (Snapshot, error) {
	var s Snapshot
	err := db.QueryRow(ctx, `
		SELECT count(*), coalesce(sum(workouts), 0), coalesce(sum(sets), 0), coalesce(sum(volume_g), 0),
		       md5(coalesce(string_agg(concat_ws('|', user_id, week_start, workouts, sets, volume_g), ',' ORDER BY user_id, week_start), ''))
		FROM weekly_stats`).Scan(&s.Rows, &s.Workouts, &s.Sets, &s.VolumeG, &s.MD5)
	return s, err
}

// WriteJSON writes v to path.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadJSON reads path into v.
func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
