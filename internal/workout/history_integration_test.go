package workout_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

const hipThrust = "Barbell_Hip_Thrust" // legs

// logSquat logs 11/9/7 at the stored plan's weights: below every target, never a failure (≥ 10 on
// set 1), so the weight holds and no session progresses on the one before (R6, R11).
func (e *env) logSquat(t *testing.T, c *http.Client, w workout.Workout) {
	t.Helper()
	ex := exercise(t, w, squat)
	for n, reps := range []int{11, 9, 7} {
		e.mustLogSet(t, c, w.ID, squat, n+1, ex.Plan.Sets[n].WeightG, reps)
	}
}

func reviewFor(t *testing.T, e *env, c *http.Client, query string) workout.WeeklyReview {
	t.Helper()
	status, b := e.do(t, c, "GET", "/review/weekly"+query, "")
	if status != http.StatusOK {
		t.Fatalf("review%s: %d %s", query, status, b)
	}
	return decode[workout.WeeklyReview](t, b)
}

func stalled(r workout.WeeklyReview, id string) bool {
	for _, s := range r.Stalled {
		if s.ExerciseID == id {
			return true
		}
	}
	return false
}

// A stall built from real sessions, end to end: four squat sessions with no progress after the
// first, a legs workout in between where squat was planned but skipped, and the first three moved
// into last week. Nothing patches the stored plans; the no-progress counter is built by the engine.
//
// What it pins down in history() (the engine's input):
//   - it returns the latest two sessions, not any two (the oldest two give no_progress 1, no stall);
//   - a workout where the exercise has a plan but no sets is not a session (the EXISTS on sets):
//     counting it would leave one usable session and no stall;
//   - the review's "as of the week's end" cut-off: last week sees only sessions 2–3 (no stall), this
//     week sees 3–4 (stall).
func TestStallFromRealSessions(t *testing.T) {
	e := setup(t)
	c := e.user(t, "stall")
	cfg := testutil.Config(t)
	e.finishDay(t, c) // push
	e.finishDay(t, c) // pull

	var sessions []progression.Session
	var squatWorkouts []string
	var nextPlan *progression.Plan
	// complete finishes a legs workout and checks the summary against the engine on the last two
	// real sessions, which is what history() must hand it.
	complete := func(step string, w workout.Workout) *progression.Plan {
		t.Helper()
		sum := e.mustComplete(t, c, w.ID)
		se := summaryFor(t, sum, squat)
		if se.ThisSession == nil {
			t.Fatalf("%s: squat has no session in the summary", step)
		}
		sessions = append(sessions, progression.Session{Date: sum.CompletedAt, Plan: se.ThisSession.Plan, Done: se.ThisSession.Done})
		want, err := progression.Next(squatCfg, sessions[max(0, len(sessions)-2):], progression.ColdStart{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*se.Next, want) {
			t.Fatalf("%s: summary next %+v\nwant progression.Next on the last two sessions %+v", step, *se.Next, want)
		}
		return se.Next
	}

	for i := range 4 {
		if i == 3 {
			// Legs workout where squat is planned but skipped: only hip thrust is logged.
			_, w := e.start(t, c)
			if w.Day != "legs" || exercise(t, w, squat).Plan == nil {
				t.Fatalf("skipped day: %s, squat plan %v", w.Day, exercise(t, w, squat).Plan)
			}
			ht := e.mustColdStart(t, c, w.ID, hipThrust, `{"dont_know":true}`)
			e.mustLogSet(t, c, w.ID, hipThrust, 1, ht.Plan.Sets[0].WeightG, ht.Plan.Sets[0].Reps)
			sum := e.mustComplete(t, c, w.ID)
			if se := summaryFor(t, sum, squat); !se.Skipped {
				t.Fatalf("squat not reported as skipped: %+v", se)
			}
			e.finishDay(t, c) // push
			e.finishDay(t, c) // pull
		}

		_, w := e.start(t, c)
		if w.Day != "legs" {
			t.Fatalf("session %d: %s day, want legs", i+1, w.Day)
		}
		if i == 0 {
			e.mustColdStart(t, c, w.ID, squat, `{"usual_kg":60}`)
			_, w = e.start(t, c) // resume, now with the plan
		} else if got := exercise(t, w, squat).Plan; !reflect.DeepEqual(got, nextPlan) {
			// The skipped workout must not change the plan: it is not a session.
			t.Fatalf("session %d: stored plan %+v\nwant the last summary's next %+v", i+1, got, nextPlan)
		}
		e.logSquat(t, c, w)
		nextPlan = complete(fmt.Sprintf("session %d", i+1), w)
		squatWorkouts = append(squatWorkouts, w.ID)

		// The counter climbs 0, 1, 2 from real sessions, then the 4th session flags the stall.
		switch i {
		case 0, 1, 2:
			if nextPlan.Stalled || nextPlan.NoProgress != i {
				t.Fatalf("after session %d: stalled=%v no_progress=%d, want false/%d", i+1, nextPlan.Stalled, nextPlan.NoProgress, i)
			}
		case 3:
			if !nextPlan.Stalled || !strings.Contains(nextPlan.Summary, "stalled 3 sessions") || nextPlan.Sets[0].WeightG != 57500 {
				t.Fatalf("after session 4: %+v, want a stall with a one-step drop to 57.5 kg", *nextPlan)
			}
		}
		if i < 3 {
			e.finishDay(t, c) // push
			e.finishDay(t, c) // pull
		}
	}

	// Sessions 1–3 move into last week (Tue–Thu, in order); the skipped workout and session 4 stay
	// in this week, completed just now.
	today := cfg.Today(time.Now())
	lastMon := config.WeekStart(today).AddDate(0, 0, -7)
	for i, id := range squatWorkouts[:3] {
		e.setCompletedAt(t, id, time.Date(lastMon.Year(), lastMon.Month(), lastMon.Day()+1+i, 18, 0, 0, 0, cfg.Location))
	}

	this := reviewFor(t, e, c, "")
	if !stalled(this, squat) {
		t.Errorf("this week: squat not stalled: %+v", this.Stalled)
	}
	if stalled(this, hipThrust) {
		t.Errorf("this week: hip thrust stalled after one session")
	}
	last := reviewFor(t, e, c, "?date="+lastMon.AddDate(0, 0, 6).Format(time.DateOnly))
	if last.WeekStart != lastMon.Format(time.DateOnly) {
		t.Fatalf("last week review is for %s", last.WeekStart)
	}
	// As of last Sunday the latest two sessions are 2 and 3: no_progress reaches only 2.
	if stalled(last, squat) {
		t.Errorf("last week: squat stalled, but session 4 is after the week's end: %+v", last.Stalled)
	}
}

// Two sessions completed at the same instant are ordered by started_at (the later start is the
// later session). Checked both ways, so the tie-break, not row order, decides.
func TestHistoryTieBrokenByStartedAt(t *testing.T) {
	e := setup(t)
	c := e.user(t, "tie")
	e.finishDay(t, c) // push
	e.finishDay(t, c) // pull

	// Session A: calibration at 60/62.5/65, all completed → next is 62.5 kg (R13, R14).
	_, a := e.start(t, c)
	e.mustColdStart(t, c, a.ID, squat, `{"usual_kg":60}`)
	_, a = e.start(t, c)
	exA := exercise(t, a, squat)
	for n, reps := range []int{12, 10, 8} {
		e.mustLogSet(t, c, a.ID, squat, n+1, exA.Plan.Sets[n].WeightG, reps)
	}
	e.mustComplete(t, c, a.ID)
	e.finishDay(t, c)
	e.finishDay(t, c)

	// Session B: building at 62.5, set 1 gets 13 → next is 65 kg (R5).
	_, b := e.start(t, c)
	exB := exercise(t, b, squat)
	for n, reps := range []int{13, 10, 8} {
		e.mustLogSet(t, c, b.ID, squat, n+1, exB.Plan.Sets[n].WeightG, reps)
	}
	sumB := e.mustComplete(t, c, b.ID)
	afterB := summaryFor(t, sumB, squat).Next
	if afterB.Sets[0].WeightG != 65000 {
		t.Fatalf("after B: %d g, want 65000", afterB.Sets[0].WeightG)
	}
	e.finishDay(t, c)
	e.finishDay(t, c) // the next workout is legs again

	tie := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	set := func(id string, startedBefore time.Duration) {
		t.Helper()
		if _, err := e.db.Exec(context.Background(),
			`UPDATE workouts SET completed_at = $2::timestamptz, started_at = $2::timestamptz - make_interval(secs => $3) WHERE id = $1::uuid`,
			id, tie, startedBefore.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	squatNext := func() progression.Plan {
		t.Helper()
		p := e.next(t, c)
		if p.Day != "legs" {
			t.Fatalf("next is %s, want legs", p.Day)
		}
		return *exercisePreview(t, p, squat).Plan
	}

	// A started first: B is the last session → 65 kg, exactly what B's summary said.
	set(a.ID, 2*time.Hour)
	set(b.ID, time.Hour)
	if got := squatNext(); !reflect.DeepEqual(got, *afterB) {
		t.Fatalf("A started first: next %+v\nwant B's summary next %+v", got, *afterB)
	}

	// B started first: now A is the last session, so the engine starts from A's calibration.
	set(a.ID, time.Hour)
	set(b.ID, 2*time.Hour)
	if got := squatNext(); got.Sets[0].WeightG != 62500 {
		t.Fatalf("B started first: next set 1 %d g, want 62500 (from A, the later start)", got.Sets[0].WeightG)
	}
}

// The tie sits at history()'s LIMIT 2 cut-off: C is clearly the latest, A and B are tied for second.
// The inner query must keep the later-started of the two, so the engine's "previous session" is
// right. C is chosen so that the answer depends on it: C holds (keeping the no-progress counter)
// and didn't progress over B but did over A.
func TestHistoryTieAtTheLimit(t *testing.T) {
	e := setup(t)
	c := e.user(t, "tie-limit")
	e.finishDay(t, c)
	e.finishDay(t, c)

	type logged struct {
		w workout.Workout
		s progression.Session
	}
	session := func(sets [][2]int, cold bool) logged {
		t.Helper()
		_, w := e.start(t, c)
		if cold {
			e.mustColdStart(t, c, w.ID, squat, `{"usual_kg":60}`)
			_, w = e.start(t, c)
		}
		for n, s := range sets {
			e.mustLogSet(t, c, w.ID, squat, n+1, s[0], s[1])
		}
		se := summaryFor(t, e.mustComplete(t, c, w.ID), squat)
		e.finishDay(t, c)
		e.finishDay(t, c)
		return logged{w, progression.Session{Plan: se.ThisSession.Plan, Done: se.ThisSession.Done}}
	}
	// A: calibration, all completed → building at 62.5.
	a := session([][2]int{{60000, 12}, {62500, 10}, {65000, 8}}, true)
	// B: 62.5 × 13 → increase to 65 (31 reps in all).
	b := session([][2]int{{62500, 13}, {65000, 10}, {67500, 8}}, false)
	// C: back at 62.5 × 11 (25 reps): no progress over B, progress over A; building zone → hold.
	cc := session([][2]int{{62500, 11}, {65000, 8}, {67500, 6}}, false)

	tie := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	set := func(id string, completed time.Time, startedBefore time.Duration) {
		t.Helper()
		if _, err := e.db.Exec(context.Background(),
			`UPDATE workouts SET completed_at = $2::timestamptz, started_at = $2::timestamptz - make_interval(secs => $3) WHERE id = $1::uuid`,
			id, completed, startedBefore.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(prev logged) progression.Plan {
		t.Helper()
		prev.s.Date, cc.s.Date = tie, tie.Add(24*time.Hour)
		p, err := progression.Next(squatCfg, []progression.Session{prev.s, cc.s}, progression.ColdStart{})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	withB, withA := expect(b), expect(a)
	if reflect.DeepEqual(withB, withA) {
		t.Fatal("test setup: the previous session doesn't change the plan, so the tie isn't tested")
	}
	squatNext := func() progression.Plan {
		t.Helper()
		return *exercisePreview(t, e.next(t, c), squat).Plan
	}

	set(cc.w.ID, tie.Add(24*time.Hour), time.Hour)
	// B started later than A: B is the second-latest session.
	set(a.w.ID, tie, 2*time.Hour)
	set(b.w.ID, tie, time.Hour)
	if got := squatNext(); !reflect.DeepEqual(got, withB) {
		t.Fatalf("B started later: next %+v\nwant Next(B, C) %+v", got, withB)
	}
	// A started later than B: now A is.
	set(a.w.ID, tie, time.Hour)
	set(b.w.ID, tie, 2*time.Hour)
	if got := squatNext(); !reflect.DeepEqual(got, withA) {
		t.Fatalf("A started later: next %+v\nwant Next(A, C) %+v", got, withA)
	}
}
