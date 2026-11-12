package workout_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
)

// Completing a workout writes exactly one workout.logged row in the same transaction; a rolled-back
// complete (409) writes none, and a repeated complete adds none.
func TestCompleteWritesOneOutboxEvent(t *testing.T) {
	e := setup(t)
	c := e.user(t, "outbox")
	sub := e.prefix + "outbox"
	const q = `SELECT count(*) FROM outbox o JOIN users u ON u.id = o.user_id WHERE u.google_sub = $1`
	_, w := e.start(t, c)

	if status, _ := e.complete(t, c, w.ID); status != http.StatusConflict {
		t.Fatalf("complete with no sets: %d, want 409", status)
	}
	if n := e.count(t, q, sub); n != 0 {
		t.Fatalf("outbox rows after a rolled-back complete = %d, want 0", n)
	}

	ex := e.mustColdStart(t, c, w.ID, lateral, `{"usual_kg":5}`)
	e.mustLogSet(t, c, w.ID, lateral, 1, ex.Plan.Sets[0].WeightG, 12)
	e.mustLogSet(t, c, w.ID, lateral, 2, ex.Plan.Sets[1].WeightG, 10)
	sum := e.mustComplete(t, c, w.ID)
	e.mustComplete(t, c, w.ID)
	if n := e.count(t, q, sub); n != 1 {
		t.Fatalf("outbox rows after two completes = %d, want 1", n)
	}

	var typ, uid string
	var version int
	var payload []byte
	if err := e.db.QueryRow(context.Background(), `
		SELECT o.type, o.version, o.user_id::text, o.payload FROM outbox o JOIN users u ON u.id = o.user_id
		WHERE u.google_sub = $1`, sub).Scan(&typ, &version, &uid, &payload); err != nil {
		t.Fatal(err)
	}
	var ev events.WorkoutLogged
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatal(err)
	}
	if typ != events.TypeWorkoutLogged || version != 1 || ev.WorkoutID != w.ID || ev.Day != "push" ||
		!ev.CompletedAt.Equal(sum.CompletedAt) || len(ev.Sets) != 2 {
		t.Fatalf("event %s v%d: %+v", typ, version, ev)
	}
	want := events.LoggedSet{ExerciseID: lateral, SetNumber: 2, WeightG: ex.Plan.Sets[1].WeightG, Reps: 10}
	if ev.Sets[1] != want {
		t.Fatalf("set 2 = %+v, want %+v", ev.Sets[1], want)
	}
}
