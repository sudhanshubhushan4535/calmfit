package workout_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/review"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

func TestWeeklyReview(t *testing.T) {
	e := setup(t)
	c := e.user(t, "review") // days_per_week 3
	cfg := testutil.Config(t)
	today := cfg.Today(time.Now())
	lastMon := config.WeekStart(today).AddDate(0, 0, -7)
	lastWeek := func(hour int) time.Time {
		return time.Date(lastMon.Year(), lastMon.Month(), lastMon.Day()+2, hour, 0, 0, 0, cfg.Location)
	}

	// Last week: push (incline 20 kg × 10), pull, legs (squat 60 kg × 11).
	_, w1 := e.start(t, c)
	e.mustColdStart(t, c, w1.ID, inclinePress, `{"usual_kg":20}`)
	e.mustLogSet(t, c, w1.ID, inclinePress, 1, 20000, 10)
	e.mustComplete(t, c, w1.ID)
	w2 := e.finishDay(t, c)
	_, w3 := e.start(t, c)
	e.mustColdStart(t, c, w3.ID, squat, `{"usual_kg":60}`)
	e.mustLogSet(t, c, w3.ID, squat, 1, 60000, 11)
	e.mustComplete(t, c, w3.ID)
	for i, id := range []string{w1.ID, w2.ID, w3.ID} {
		e.setCompletedAt(t, id, lastWeek(8+i))
	}

	// This week: push (incline up to 22.5 kg × 8), pull, legs (squat repeats 60 × 11).
	_, w4 := e.start(t, c)
	e.mustLogSet(t, c, w4.ID, inclinePress, 1, 22500, 8)
	e.mustComplete(t, c, w4.ID)
	e.finishDay(t, c)
	_, w6 := e.start(t, c)
	// Make this the third session in a row without progress, so the engine flags a stall (R12).
	if _, err := e.db.Exec(context.Background(), `
		UPDATE workout_exercises SET plan = plan || '{"phase":"normal","no_progress":2}'::jsonb
		WHERE workout_id = $1::uuid AND exercise_id = $2`, w6.ID, squat); err != nil {
		t.Fatal(err)
	}
	e.mustLogSet(t, c, w6.ID, squat, 1, 60000, 11)
	e.mustComplete(t, c, w6.ID)

	get := func(query string) workout.WeeklyReview {
		t.Helper()
		status, b := e.do(t, c, "GET", "/review/weekly"+query, "")
		if status != http.StatusOK {
			t.Fatalf("review%s: %d %s", query, status, b)
		}
		return decode[workout.WeeklyReview](t, b)
	}

	r := get("")
	if r.WeekStart != config.WeekStart(today).Format(time.DateOnly) || r.Done != 3 || r.Planned != 3 {
		t.Fatalf("this week: %+v", r)
	}
	if len(r.WentUp) != 1 || r.WentUp[0].ExerciseID != inclinePress ||
		r.WentUp[0].From != (review.Set{WeightG: 20000, Reps: 10}) || r.WentUp[0].To != (review.Set{WeightG: 22500, Reps: 8}) {
		t.Fatalf("went up = %+v", r.WentUp)
	}
	if len(r.Stalled) != 1 || r.Stalled[0].ExerciseID != squat {
		t.Fatalf("stalled = %+v", r.Stalled)
	}
	if !strings.Contains(r.Tip, "stalled") || r.Plate.GoodDays != 0 || r.Plate.Streak != 0 {
		t.Fatalf("tip/plate: %q %+v", r.Tip, r.Plate)
	}

	// Last week: 3 of 3, nothing to compare against yet, no stall; no plate entries are neutral, so no plate tip.
	r = get("?date=" + lastMon.AddDate(0, 0, 6).Format(time.DateOnly))
	if r.WeekStart != lastMon.Format(time.DateOnly) || r.Done != 3 || len(r.WentUp) != 0 || len(r.Stalled) != 0 ||
		r.Plate.Days != 0 || !strings.HasPrefix(r.Tip, "Solid week") {
		t.Fatalf("last week: %+v", r)
	}

	if status, _ := e.do(t, c, "GET", "/review/weekly?date="+today.AddDate(0, 0, 1).Format(time.DateOnly), ""); status != http.StatusBadRequest {
		t.Errorf("future date: %d, want 400", status)
	}
	other := e.user(t, "review-other")
	if r := get2(t, e, other); r.Done != 0 || len(r.WentUp) != 0 || !strings.HasPrefix(r.Tip, "No workouts") {
		t.Errorf("other user sees data: %+v", r)
	}
	fresh := testutil.Login(t, e.url, e.fv, e.prefix+"review-new")
	if status, _ := e.do(t, fresh, "GET", "/review/weekly", ""); status != http.StatusConflict {
		t.Errorf("not onboarded: %d, want 409", status)
	}
}

func get2(t *testing.T, e *env, c *http.Client) workout.WeeklyReview {
	t.Helper()
	status, b := e.do(t, c, "GET", "/review/weekly", "")
	if status != http.StatusOK {
		t.Fatalf("review: %d %s", status, b)
	}
	return decode[workout.WeeklyReview](t, b)
}
