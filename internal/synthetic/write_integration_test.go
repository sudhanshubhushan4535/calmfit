package synthetic

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

// Writes 20 users to calmfit_test, checks the row counts, then signs one in through test auth
// and asks the real router for its next workout and weekly review.
func TestWriteThenServe(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	cfg := testutil.Config(t)
	if _, err := Reset(ctx, db); err != nil { // leftovers from an aborted run
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := Reset(ctx, db); err != nil {
			t.Errorf("reset: %v", err)
		}
	})

	tpl, err := LoadTemplate(ctx, db, "ppl")
	if err != nil {
		t.Fatal(err)
	}
	p := Params{Users: 20, Seed: 42, Anchor: cfg.Today(time.Now()), Loc: cfg.Location}
	want, err := Count(p, tpl)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Write(ctx, db, p, tpl, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("wrote %+v, planned %+v", got, want)
	}
	var dbCounts Counts
	if err := db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM users u WHERE google_sub ~ $1),
		       (SELECT count(*) FROM user_programs up JOIN users u ON u.id = up.user_id WHERE google_sub ~ $1),
		       (SELECT count(*) FROM workouts w JOIN users u ON u.id = w.user_id WHERE google_sub ~ $1),
		       (SELECT count(*) FROM workout_exercises we JOIN workouts w ON w.id = we.workout_id JOIN users u ON u.id = w.user_id WHERE google_sub ~ $1),
		       (SELECT count(*) FROM workout_sets s JOIN workouts w ON w.id = s.workout_id JOIN users u ON u.id = w.user_id WHERE google_sub ~ $1),
		       (SELECT count(*) FROM plate_checks pc JOIN users u ON u.id = pc.user_id WHERE google_sub ~ $1)`, SubPattern).
		Scan(&dbCounts.Users, &dbCounts.UserPrograms, &dbCounts.Workouts, &dbCounts.WorkoutExercises, &dbCounts.WorkoutSets, &dbCounts.PlateChecks); err != nil {
		t.Fatal(err)
	}
	if dbCounts != want {
		t.Fatalf("DB has %+v, want %+v", dbCounts, want)
	}
	var outbox int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM outbox o JOIN users u ON u.id = o.user_id WHERE google_sub ~ $1`, SubPattern).Scan(&outbox)
	if outbox != 0 {
		t.Fatalf("%d outbox rows: synthetic data must bypass the outbox", outbox)
	}

	srv := httptest.NewServer(server.New(db, auth.TestVerifier{Next: testutil.NewFakeVerifier()}, "", cfg))
	defer srv.Close()
	// The user with the most workouts: the engine runs on its real history.
	var sub string
	if err := db.QueryRow(ctx, `
		SELECT u.google_sub FROM users u JOIN workouts w ON w.user_id = u.id
		WHERE u.google_sub ~ $1 GROUP BY u.google_sub ORDER BY count(*) DESC LIMIT 1`, SubPattern).Scan(&sub); err != nil {
		t.Fatal(err)
	}
	c := testutil.LoginCredential(t, srv.URL, "test:"+sub)
	var next struct {
		Exercises []struct {
			ID               string `json:"id"`
			NeedsUsualWeight bool   `json:"needs_usual_weight"`
		} `json:"exercises"`
	}
	if err := json.Unmarshal(testutil.Do(t, c, "GET", srv.URL+"/workouts/next", ""), &next); err != nil {
		t.Fatal(err)
	}
	for _, e := range next.Exercises {
		if e.NeedsUsualWeight {
			t.Errorf("%s: a user with long history still needs a cold start", e.ID)
		}
	}
	testutil.Do(t, c, "GET", srv.URL+"/review/weekly", "")
}
