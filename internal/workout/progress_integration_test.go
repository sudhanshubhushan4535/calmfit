package workout_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

// setCompletedAt moves a completed workout's completion time (tests only).
func (e *env) setCompletedAt(t *testing.T, wid string, at time.Time) {
	t.Helper()
	if _, err := e.db.Exec(context.Background(),
		`UPDATE workouts SET completed_at = $2::timestamptz, started_at = $2::timestamptz - interval '1 hour' WHERE id = $1::uuid`, wid, at); err != nil {
		t.Fatal(err)
	}
}

func TestExerciseProgressIsSetOneOnly(t *testing.T) {
	e := setup(t)
	c := e.user(t, "progress")
	cfg := testutil.Config(t)

	// Push day 1: set 1 at 20 kg × 10, set 2 at 22.5 kg (must not show up).
	_, w1 := e.start(t, c)
	e.mustColdStart(t, c, w1.ID, inclinePress, `{"usual_kg":20}`)
	e.mustLogSet(t, c, w1.ID, inclinePress, 1, 20000, 10)
	e.mustLogSet(t, c, w1.ID, inclinePress, 2, 22500, 8)
	e.mustComplete(t, c, w1.ID)
	e.finishDay(t, c)
	e.finishDay(t, c)
	// Push day 2: set 1 at 22.5 kg × 8.
	_, w2 := e.start(t, c)
	e.mustLogSet(t, c, w2.ID, inclinePress, 1, 22500, 8)
	e.mustComplete(t, c, w2.ID)

	// 2026-03-01 20:00 UTC is 2026-03-02 01:30 IST: the point's date is the local date.
	e.setCompletedAt(t, w1.ID, time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC))

	status, b := e.do(t, c, "GET", "/progress/"+inclinePress, "")
	if status != http.StatusOK {
		t.Fatalf("progress: %d %s", status, b)
	}
	p := decode[workout.ExerciseProgress](t, b)
	today := cfg.Today(time.Now()).Format(time.DateOnly)
	want := []workout.ProgressPoint{{Date: "2026-03-02", WeightG: 20000, Reps: 10}, {Date: today, WeightG: 22500, Reps: 8}}
	if len(p.Points) != 2 || p.Points[0] != want[0] || p.Points[1] != want[1] || p.Name == "" {
		t.Fatalf("points = %+v, want %+v", p.Points, want)
	}

	// Another user sees nothing; unknown exercise is 404.
	other := e.user(t, "progress-other")
	if status, b := e.do(t, other, "GET", "/progress/"+inclinePress, ""); status != http.StatusOK ||
		len(decode[workout.ExerciseProgress](t, b).Points) != 0 {
		t.Fatalf("other user: %d %s", status, b)
	}
	if status, _ := e.do(t, c, "GET", "/progress/Nope", ""); status != http.StatusNotFound {
		t.Fatalf("unknown exercise: %d", status)
	}
}

func TestWeeklyProgressBucketsByLocalWeek(t *testing.T) {
	e := setup(t)
	c := e.user(t, "weekly") // days_per_week 3
	cfg := testutil.Config(t)
	thisMon := config.WeekStart(cfg.Today(time.Now()))
	at := func(d time.Time, hh, mm int) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, cfg.Location)
	}

	a := e.finishDay(t, c)
	b := e.finishDay(t, c)
	d := e.finishDay(t, c)
	// Last week's Sunday 23:50 IST stays in last week, even though it's Sunday 18:20 UTC.
	e.setCompletedAt(t, a.ID, at(thisMon.AddDate(0, 0, -1), 23, 50))
	// Last week's Monday 00:10 IST is last week, though in UTC it's the Sunday before.
	e.setCompletedAt(t, b.ID, at(thisMon.AddDate(0, 0, -7), 0, 10))
	// Three weeks ago, Wednesday.
	e.setCompletedAt(t, d.ID, at(thisMon.AddDate(0, 0, -19), 12, 0))

	status, body := e.do(t, c, "GET", "/progress/weekly?weeks=4", "")
	if status != http.StatusOK {
		t.Fatalf("weekly: %d %s", status, body)
	}
	got := decode[struct{ Weeks []workout.WeekCount }](t, body).Weeks
	wantDone := []int{1, 0, 2, 0}
	if len(got) != 4 {
		t.Fatalf("weeks = %+v", got)
	}
	for i, w := range got {
		start := thisMon.AddDate(0, 0, -7*(3-i)).Format(time.DateOnly)
		if w.WeekStart != start || w.Done != wantDone[i] || w.Planned != 3 {
			t.Errorf("week %d = %+v, want start %s done %d planned 3", i, w, start, wantDone[i])
		}
	}

	for _, q := range []string{"0", "27", "x"} {
		if status, _ := e.do(t, c, "GET", "/progress/weekly?weeks="+q, ""); status != http.StatusBadRequest {
			t.Errorf("weeks=%s: %d, want 400", q, status)
		}
	}
	fresh := testutil.Login(t, e.url, e.fv, e.prefix+"weekly-new")
	if status, _ := e.do(t, fresh, "GET", "/progress/weekly", ""); status != http.StatusConflict {
		t.Errorf("not onboarded: %d, want 409", status)
	}
}
