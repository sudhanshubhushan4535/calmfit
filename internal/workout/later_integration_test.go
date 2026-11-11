package workout_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

func ids(w workout.Workout) []string {
	out := make([]string, len(w.Exercises))
	for i, ex := range w.Exercises {
		out[i] = ex.ID
	}
	return out
}

func (e *env) later(t *testing.T, c *http.Client, wid, ex string) (int, []byte) {
	t.Helper()
	return e.do(t, c, "POST", "/workouts/"+wid+"/exercises/"+ex+"/later", "")
}

func (e *env) mustLater(t *testing.T, c *http.Client, wid, ex string) workout.Workout {
	t.Helper()
	status, b := e.later(t, c, wid, ex)
	if status != http.StatusOK {
		t.Fatalf("later %s: %d %s", ex, status, b)
	}
	return decode[workout.Workout](t, b)
}

func TestDoLaterMovesToEndAndIsIdempotent(t *testing.T) {
	e := setup(t)
	c := e.user(t, "later")
	_, w := e.start(t, c)
	before := ids(w)
	if before[0] != inclinePress {
		t.Fatalf("push day should start with %s, got %v", inclinePress, before)
	}
	// Give the moved exercise a plan so we can check it survives the move.
	e.mustColdStart(t, c, w.ID, inclinePress, `{"usual_kg":20}`)
	planBefore := exercise(t, mustLoad(t, e, c), inclinePress).Plan

	want := append(append([]string{}, before[1:]...), before[0])
	got := e.mustLater(t, c, w.ID, inclinePress)
	if !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("order after later = %v, want %v", ids(got), want)
	}
	if !reflect.DeepEqual(exercise(t, got, inclinePress).Plan, planBefore) {
		t.Fatalf("plan changed by do-later")
	}

	// Repeating it, or moving the last exercise, changes nothing.
	again := e.mustLater(t, c, w.ID, inclinePress)
	if !reflect.DeepEqual(ids(again), want) {
		t.Fatalf("second later changed order: %v", ids(again))
	}

	// Moving another one appends it after the first move.
	want2 := append(append([]string{}, want[1:]...), want[0])
	if got := e.mustLater(t, c, w.ID, want[0]); !reflect.DeepEqual(ids(got), want2) {
		t.Fatalf("order after second move = %v, want %v", ids(got), want2)
	}

	// The template is untouched: the next push day uses the original order.
	e.mustLogSet(t, c, w.ID, inclinePress, 1, planBefore.Sets[0].WeightG, planBefore.Sets[0].Reps)
	e.mustComplete(t, c, w.ID)
	e.finishDay(t, c) // pull
	e.finishDay(t, c) // legs
	if p := e.next(t, c); p.Day != "push" || !reflect.DeepEqual(previewIDs(p), before) {
		t.Fatalf("next push order = %s %v, want %v", p.Day, previewIDs(p), before)
	}
}

func TestDoLaterErrors(t *testing.T) {
	e := setup(t)
	a := e.user(t, "later-a")
	b := e.user(t, "later-b")
	_, w := e.start(t, a)

	if status, body := e.later(t, b, w.ID, inclinePress); status != http.StatusNotFound {
		t.Fatalf("other user: %d %s, want 404", status, body)
	}
	if status, body := e.later(t, a, w.ID, squat); status != http.StatusNotFound {
		t.Fatalf("exercise not in workout: %d %s, want 404", status, body)
	}
	if status, body := e.later(t, a, "not-a-uuid", inclinePress); status != http.StatusNotFound {
		t.Fatalf("bad id: %d %s, want 404", status, body)
	}

	ex := e.mustColdStart(t, a, w.ID, inclinePress, `{"dont_know":true}`)
	e.mustLogSet(t, a, w.ID, inclinePress, 1, ex.Plan.Sets[0].WeightG, ex.Plan.Sets[0].Reps)
	e.mustComplete(t, a, w.ID)
	if status, body := e.later(t, a, w.ID, lateral); status != http.StatusConflict {
		t.Fatalf("completed workout: %d %s, want 409", status, body)
	}
}

func mustLoad(t *testing.T, e *env, c *http.Client) workout.Workout {
	t.Helper()
	_, w := e.start(t, c) // resumes the open workout
	return w
}

func previewIDs(p workout.Preview) []string {
	out := make([]string, len(p.Exercises))
	for i, ex := range p.Exercises {
		out[i] = ex.ID
	}
	return out
}
