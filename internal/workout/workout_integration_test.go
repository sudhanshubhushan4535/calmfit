package workout_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

// Exercise ids from seed/templates.json.
const (
	inclinePress = "Incline_Dumbbell_Press" // push, dumbbell, big
	lateral      = "Side_Lateral_Raise"     // push, dumbbell, small
	pulldown     = "Close-Grip_Front_Lat_Pulldown"
	squat        = "Barbell_Squat" // legs, barbell, big, compound
)

var (
	squatCfg = progression.Config{ExerciseID: squat, Equipment: "barbell", Size: progression.Big,
		Movement: progression.Compound, Scheme: progression.Standard, RepStep: 2, WeightStepG: 2500}
	lateralCfg = progression.Config{ExerciseID: lateral, Equipment: "dumbbell", Size: progression.Small,
		Movement: progression.Isolation, Scheme: progression.Standard, RepStep: 2, WeightStepG: 2500}
)

type env struct {
	url    string
	db     *pgxpool.Pool
	fv     *testutil.FakeVerifier
	prefix string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.DB(t)
	prefix := testutil.SubPrefix(t, db)
	fv := testutil.NewFakeVerifier()
	srv := httptest.NewServer(server.New(db, fv, "test-client-id"))
	t.Cleanup(srv.Close)
	return &env{url: srv.URL, db: db, fv: fv, prefix: prefix}
}

// user signs in and onboards a fresh user.
func (e *env) user(t *testing.T, name string) *http.Client {
	t.Helper()
	c := testutil.Login(t, e.url, e.fv, e.prefix+name)
	if status, body := e.do(t, c, "POST", "/onboarding", `{"days_per_week":3}`); status != http.StatusOK {
		t.Fatalf("onboard %s: %d %s", name, status, body)
	}
	return c
}

func (e *env) do(t *testing.T, c *http.Client, method, path, body string) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.url+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %T: %v (%s)", v, err, b)
	}
	return v
}

func (e *env) start(t *testing.T, c *http.Client) (int, workout.Workout) {
	t.Helper()
	status, b := e.do(t, c, "POST", "/workouts/start", "")
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, b)
	}
	return status, decode[workout.Workout](t, b)
}

func (e *env) next(t *testing.T, c *http.Client) workout.Preview {
	t.Helper()
	status, b := e.do(t, c, "GET", "/workouts/next", "")
	if status != http.StatusOK {
		t.Fatalf("next: %d %s", status, b)
	}
	return decode[workout.Preview](t, b)
}

func (e *env) coldStart(t *testing.T, c *http.Client, wid, ex, body string) (int, []byte) {
	t.Helper()
	return e.do(t, c, "POST", "/workouts/"+wid+"/exercises/"+ex+"/cold-start", body)
}

func (e *env) mustColdStart(t *testing.T, c *http.Client, wid, ex, body string) workout.Exercise {
	t.Helper()
	status, b := e.coldStart(t, c, wid, ex, body)
	if status != http.StatusOK {
		t.Fatalf("cold start %s %s: %d %s", ex, body, status, b)
	}
	return decode[workout.Exercise](t, b)
}

func (e *env) logSet(t *testing.T, c *http.Client, wid, ex string, n, weightG, reps int) (int, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"exercise_id": ex, "set_number": n, "weight_g": weightG, "reps": reps})
	return e.do(t, c, "POST", "/workouts/"+wid+"/sets", string(body))
}

func (e *env) mustLogSet(t *testing.T, c *http.Client, wid, ex string, n, weightG, reps int) workout.Exercise {
	t.Helper()
	status, b := e.logSet(t, c, wid, ex, n, weightG, reps)
	if status != http.StatusOK {
		t.Fatalf("log %s set %d: %d %s", ex, n, status, b)
	}
	return decode[workout.Exercise](t, b)
}

func (e *env) complete(t *testing.T, c *http.Client, wid string) (int, []byte) {
	t.Helper()
	return e.do(t, c, "POST", "/workouts/"+wid+"/complete", "")
}

func (e *env) mustComplete(t *testing.T, c *http.Client, wid string) workout.Summary {
	t.Helper()
	status, b := e.complete(t, c, wid)
	if status != http.StatusOK {
		t.Fatalf("complete: %d %s", status, b)
	}
	return decode[workout.Summary](t, b)
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func exercise(t *testing.T, w workout.Workout, id string) workout.Exercise {
	t.Helper()
	for _, ex := range w.Exercises {
		if ex.ID == id {
			return ex
		}
	}
	t.Fatalf("exercise %s not in %s workout", id, w.Day)
	return workout.Exercise{}
}

func summaryFor(t *testing.T, s workout.Summary, id string) workout.SummaryExercise {
	t.Helper()
	for _, ex := range s.Exercises {
		if ex.ID == id {
			return ex
		}
	}
	t.Fatalf("exercise %s not in summary", id)
	return workout.SummaryExercise{}
}

// finishDay starts the next workout, logs one set of its first exercise (cold-starting it if needed) and completes it.
func (e *env) finishDay(t *testing.T, c *http.Client) workout.Workout {
	t.Helper()
	_, w := e.start(t, c)
	ex := w.Exercises[0]
	if ex.NeedsUsualWeight {
		ex = e.mustColdStart(t, c, w.ID, ex.ID, `{"dont_know":true}`)
	}
	e.mustLogSet(t, c, w.ID, ex.ID, 1, ex.Plan.Sets[0].WeightG, ex.Plan.Sets[0].Reps)
	e.mustComplete(t, c, w.ID)
	return w
}

func TestUnauthenticatedIs401(t *testing.T) {
	e := setup(t)
	anon := &http.Client{}
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/workouts/next", ""},
		{"POST", "/workouts/start", ""},
		{"POST", "/workouts/00000000-0000-0000-0000-000000000000/sets", `{}`},
		{"POST", "/workouts/00000000-0000-0000-0000-000000000000/exercises/x/cold-start", `{}`},
		{"POST", "/workouts/00000000-0000-0000-0000-000000000000/complete", ""},
	} {
		if status, _ := e.do(t, anon, r.method, r.path, r.body); status != http.StatusUnauthorized {
			t.Errorf("%s %s without session: %d, want 401", r.method, r.path, status)
		}
	}
}

func TestStartRequiresOnboarding(t *testing.T) {
	e := setup(t)
	c := testutil.Login(t, e.url, e.fv, e.prefix+"fresh")
	if status, b := e.do(t, c, "POST", "/workouts/start", ""); status != http.StatusConflict || !strings.Contains(string(b), "onboarding_required") {
		t.Fatalf("start before onboarding: %d %s, want 409 onboarding_required", status, b)
	}
}

func TestStartTwiceReturnsSameWorkout(t *testing.T) {
	e := setup(t)
	c := e.user(t, "twice")

	s1, w1 := e.start(t, c)
	s2, w2 := e.start(t, c)
	if s1 != http.StatusCreated || s2 != http.StatusOK {
		t.Fatalf("statuses %d, %d; want 201 then 200", s1, s2)
	}
	if w1.ID != w2.ID || w1.Day != "push" || w1.DayIndex != 0 || w1.Status != "in_progress" || len(w1.Exercises) != 5 {
		t.Fatalf("w1 = %s %s/%d %s (%d ex), w2 = %s", w1.ID, w1.Day, w1.DayIndex, w1.Status, len(w1.Exercises), w2.ID)
	}
	if n := e.count(t, `SELECT count(*) FROM workouts w JOIN users u ON u.id = w.user_id WHERE u.google_sub = $1`, e.prefix+"twice"); n != 1 {
		t.Fatalf("%d workouts, want 1", n)
	}
	if p := e.next(t, c); p.InProgressWorkoutID == nil || *p.InProgressWorkoutID != w1.ID {
		t.Fatalf("preview in_progress_workout_id = %v, want %s", p.InProgressWorkoutID, w1.ID)
	}
}

func TestColdStartFlow(t *testing.T) {
	e := setup(t)
	c := e.user(t, "cold")
	_, w := e.start(t, c)
	ex := exercise(t, w, inclinePress)
	if !ex.NeedsUsualWeight || ex.Plan != nil {
		t.Fatalf("fresh exercise: needs_usual_weight=%v plan=%v, want true/nil", ex.NeedsUsualWeight, ex.Plan)
	}

	if status, b := e.logSet(t, c, w.ID, inclinePress, 1, 20000, 12); status != http.StatusConflict || !strings.Contains(string(b), "needs_usual_weight") {
		t.Fatalf("log before cold start: %d %s, want 409 needs_usual_weight", status, b)
	}
	for _, body := range []string{`{}`, `{"usual_kg":20,"dont_know":true}`, `{"usual_kg":0}`, `{"usual_kg":-5}`, `{"dont_know":false}`, `{"usual_kg":"20"}`, `{"kg":20}`} {
		if status, b := e.coldStart(t, c, w.ID, inclinePress, body); status != http.StatusBadRequest {
			t.Errorf("cold start %s: %d %s, want 400", body, status, b)
		}
	}
	if status, _ := e.coldStart(t, c, w.ID, squat, `{"dont_know":true}`); status != http.StatusNotFound {
		t.Fatalf("cold start on an exercise not in the workout: %d, want 404", status)
	}

	// 21 kg rounds down to the 2.5 kg step (R13).
	got := e.mustColdStart(t, c, w.ID, inclinePress, `{"usual_kg":21}`)
	if got.NeedsUsualWeight || got.Plan == nil || got.Plan.Phase != progression.PhaseCalibration || got.Plan.Sets[0].WeightG != 20000 {
		t.Fatalf("after usual 21 kg: %+v, want calibration at 20 kg", got.Plan)
	}
	want, _ := progression.Next(progression.Config{ExerciseID: inclinePress, Equipment: "dumbbell", Size: progression.Big,
		Movement: progression.Compound, Scheme: progression.Standard, RepStep: 2, WeightStepG: 2500}, nil, progression.ColdStart{UsualWeightG: 21000})
	if !reflect.DeepEqual(*got.Plan, want) || !reflect.DeepEqual(got.Remaining, want.Sets) {
		t.Fatalf("stored plan %+v\nwant %+v", *got.Plan, want)
	}

	// The answer can change until a set is logged.
	got = e.mustColdStart(t, c, w.ID, inclinePress, `{"dont_know":true}`)
	if got.Plan.Sets[0].WeightG != 2500 {
		t.Fatalf("don't know: set 1 = %d g, want 2500 (lightest dumbbell)", got.Plan.Sets[0].WeightG)
	}
	e.mustLogSet(t, c, w.ID, inclinePress, 1, 2500, 12)
	if status, b := e.coldStart(t, c, w.ID, inclinePress, `{"usual_kg":30}`); status != http.StatusConflict || !strings.Contains(string(b), "sets_already_logged") {
		t.Fatalf("cold start after a set: %d %s, want 409 sets_already_logged", status, b)
	}

	// Once there is history, the next workout plans from it and refuses a cold start.
	e.mustComplete(t, c, w.ID)
	for range 2 {
		e.finishDay(t, c) // pull, legs
	}
	_, w2 := e.start(t, c)
	if ex := exercise(t, w2, inclinePress); ex.NeedsUsualWeight || ex.Plan == nil {
		t.Fatalf("second push: %+v, want a plan from history", ex)
	}
	if status, b := e.coldStart(t, c, w2.ID, inclinePress, `{"dont_know":true}`); status != http.StatusConflict || !strings.Contains(string(b), "exercise_has_history") {
		t.Fatalf("cold start with history: %d %s, want 409", status, b)
	}
}

func TestSetUpsertIsIdempotent(t *testing.T) {
	e := setup(t)
	c := e.user(t, "sets")
	_, w := e.start(t, c)
	ex := e.mustColdStart(t, c, w.ID, lateral, `{"usual_kg":5}`) // 5 / 7.5 / 10 at 12/10/8

	rows := func() int {
		return e.count(t, `SELECT count(*) FROM workout_sets WHERE workout_id = $1::uuid AND exercise_id = $2`, w.ID, lateral)
	}
	a := e.mustLogSet(t, c, w.ID, lateral, 1, 5000, 11)
	b := e.mustLogSet(t, c, w.ID, lateral, 1, 5000, 11) // retry
	if rows() != 1 || !reflect.DeepEqual(a.Remaining, b.Remaining) {
		t.Fatalf("after retry: %d rows, remaining %v vs %v; want 1 row and identical targets", rows(), a.Remaining, b.Remaining)
	}
	// R10 (small muscle): set 1 missed, so set 2 holds at 5 kg × 12 and set 3 is one step above.
	want, _ := progression.AdjustInSession(lateralCfg, *ex.Plan, []progression.Set{{WeightG: 5000, Reps: 11}})
	if !reflect.DeepEqual(b.Remaining, want) || b.Remaining[0].WeightG != 5000 || b.Remaining[0].Reps != 12 || b.Remaining[1].WeightG != 7500 {
		t.Fatalf("remaining = %+v, want %+v", b.Remaining, want)
	}

	// Edit: same set number replaces the row.
	edited := e.mustLogSet(t, c, w.ID, lateral, 1, 5000, 12)
	if rows() != 1 || len(edited.Sets) != 1 || edited.Sets[0].Reps != 12 || !reflect.DeepEqual(edited.Remaining, ex.Plan.Sets[1:]) {
		t.Fatalf("after edit: rows=%d sets=%+v remaining=%+v", rows(), edited.Sets, edited.Remaining)
	}

	if status, _ := e.logSet(t, c, w.ID, lateral, 3, 10000, 8); status != http.StatusBadRequest {
		t.Fatalf("set 3 before set 2: %d, want 400", status)
	}
	for _, bad := range []struct{ n, g, reps int }{{0, 5000, 10}, {4, 5000, 10}, {2, 0, 10}, {2, -2500, 10}, {2, 5000, 0}, {2, 5000, 51}} {
		if status, _ := e.logSet(t, c, w.ID, lateral, bad.n, bad.g, bad.reps); status != http.StatusBadRequest {
			t.Errorf("set %+v: %d, want 400", bad, status)
		}
	}
	if status, _ := e.logSet(t, c, w.ID, squat, 1, 60000, 10); status != http.StatusNotFound {
		t.Fatalf("set for an exercise not in the workout: %d, want 404", status)
	}

	full := e.mustLogSet(t, c, w.ID, lateral, 2, 7500, 10)
	full = e.mustLogSet(t, c, w.ID, lateral, 3, 10000, 8)
	if len(full.Sets) != 3 || len(full.Remaining) != 0 {
		t.Fatalf("after 3 sets: %d sets, %d remaining", len(full.Sets), len(full.Remaining))
	}

	e.mustComplete(t, c, w.ID)
	if status, b := e.logSet(t, c, w.ID, lateral, 3, 10000, 9); status != http.StatusConflict || !strings.Contains(string(b), "workout_completed") {
		t.Fatalf("log on completed workout: %d %s, want 409", status, b)
	}
}

func TestCompleteTwiceAdvancesOnce(t *testing.T) {
	e := setup(t)
	c := e.user(t, "complete")
	_, w := e.start(t, c)

	if status, b := e.complete(t, c, w.ID); status != http.StatusConflict || !strings.Contains(string(b), "no_sets_logged") {
		t.Fatalf("complete with no sets: %d %s, want 409", status, b)
	}
	ex := e.mustColdStart(t, c, w.ID, lateral, `{"usual_kg":5}`)
	e.mustLogSet(t, c, w.ID, lateral, 1, ex.Plan.Sets[0].WeightG, 12)

	_, first := e.complete(t, c, w.ID)
	status, second := e.complete(t, c, w.ID)
	if status != http.StatusOK || !bytes.Equal(first, second) {
		t.Fatalf("second complete: %d\n%s\nwant identical to\n%s", status, second, first)
	}
	idx := e.count(t, `SELECT up.last_completed_index FROM user_programs up JOIN users u ON u.id = up.user_id WHERE u.google_sub = $1`, e.prefix+"complete")
	if idx != 0 {
		t.Fatalf("last_completed_index = %d, want 0", idx)
	}
	if p := e.next(t, c); p.Day != "pull" || p.InProgressWorkoutID != nil {
		t.Fatalf("next = %s (in progress %v), want pull and none open", p.Day, p.InProgressWorkoutID)
	}

	sum := decode[workout.Summary](t, first)
	if sum.Day != "push" || len(sum.Exercises) != 5 {
		t.Fatalf("summary %s with %d exercises", sum.Day, len(sum.Exercises))
	}
	for _, se := range sum.Exercises {
		if (se.ID == lateral) == se.Skipped {
			t.Errorf("%s skipped=%v", se.ID, se.Skipped)
		}
	}
}

func TestRotationPushPullLegsPush(t *testing.T) {
	e := setup(t)
	c := e.user(t, "rotation")
	for i, want := range []string{"push", "pull", "legs", "push"} {
		if p := e.next(t, c); p.Day != want {
			t.Fatalf("step %d: preview %s, want %s", i, p.Day, want)
		}
		if w := e.finishDay(t, c); w.Day != want || w.DayIndex != i%3 {
			t.Fatalf("step %d: started %s/%d, want %s/%d", i, w.Day, w.DayIndex, want, i%3)
		}
	}
}

func TestUsersCannotTouchOthersWorkouts(t *testing.T) {
	e := setup(t)
	a := e.user(t, "a")
	b := e.user(t, "b")

	_, wa := e.start(t, a)
	exA := e.mustColdStart(t, a, wa.ID, lateral, `{"usual_kg":5}`)
	e.mustLogSet(t, a, wa.ID, lateral, 1, 5000, 12)

	if status, _ := e.logSet(t, b, wa.ID, lateral, 1, 50000, 1); status != http.StatusNotFound {
		t.Errorf("B logs on A's workout: %d, want 404", status)
	}
	if status, _ := e.coldStart(t, b, wa.ID, inclinePress, `{"dont_know":true}`); status != http.StatusNotFound {
		t.Errorf("B cold-starts on A's workout: %d, want 404", status)
	}
	if status, _ := e.complete(t, b, wa.ID); status != http.StatusNotFound {
		t.Errorf("B completes A's workout: %d, want 404", status)
	}
	if status, _ := e.complete(t, b, "not-a-uuid"); status != http.StatusNotFound {
		t.Errorf("malformed id: %d, want 404", status)
	}

	// B's own start is a separate workout; A's is untouched.
	_, wb := e.start(t, b)
	if wb.ID == wa.ID {
		t.Fatal("B got A's workout")
	}
	_, again := e.start(t, a)
	got := exercise(t, again, lateral)
	if again.ID != wa.ID || again.Status != "in_progress" || len(got.Sets) != 1 || got.Sets[0].WeightG != 5000 ||
		!reflect.DeepEqual(got.Plan, exA.Plan) || !exercise(t, again, inclinePress).NeedsUsualWeight {
		t.Fatalf("A's workout changed: %+v", again)
	}
}

// TestFullEngineCycle drives Barbell_Squat through calibration -> building -> shifted -> normal -> building via the
// API. Each summary must equal progression.Next over the sessions the API reported, and each next start must store it.
func TestFullEngineCycle(t *testing.T) {
	e := setup(t)
	c := e.user(t, "cycle")
	e.finishDay(t, c) // push
	e.finishDay(t, c) // pull

	type expect struct {
		reps  []int // logged at the plan's weights
		phase progression.Phase
		set1  int
		why   string
	}
	steps := []expect{
		// Calibration 60/62.5/65 all completed -> heaviest 65, capped one step above 60 (R13, R14).
		{[]int{12, 10, 8}, progression.PhaseBuilding, 62500, "+2.5 kg: first session, you completed set 3 at 65 kg"},
		// Building, set 1 reached only 10 -> shifted targets (R8).
		{[]int{10, 8, 6}, progression.PhaseShifted, 62500, "Same weight, targets 10/8/6"},
		// Shifted, set 1 hit 12 -> back to normal at the same weight.
		{[]int{12, 8, 6}, progression.PhaseNormal, 62500, "Same weight, back to 12/10/8: set 1 hit 12"},
		// Normal, set 1 above the top -> increase (R5).
		{[]int{13, 10, 8}, progression.PhaseBuilding, 65000, "+2.5 kg: you hit 13 reps last time"},
	}

	var sessions []progression.Session
	var nextPlan *progression.Plan
	for i, st := range steps {
		if p := e.next(t, c); p.Day != "legs" {
			t.Fatalf("step %d: preview %s, want legs", i, p.Day)
		} else if i > 0 && !reflect.DeepEqual(exercisePreview(t, p, squat).Plan, nextPlan) {
			t.Fatalf("step %d: preview plan differs from last summary's next", i)
		}
		_, w := e.start(t, c)
		ex := exercise(t, w, squat)
		if i == 0 {
			ex = e.mustColdStart(t, c, w.ID, squat, `{"usual_kg":60}`)
		} else if !reflect.DeepEqual(ex.Plan, nextPlan) {
			t.Fatalf("step %d: stored plan %+v\nwant last summary's next %+v", i, ex.Plan, nextPlan)
		}
		for n, reps := range st.reps {
			e.mustLogSet(t, c, w.ID, squat, n+1, ex.Plan.Sets[n].WeightG, reps)
		}
		sum := e.mustComplete(t, c, w.ID)
		se := summaryFor(t, sum, squat)

		sessions = append(sessions, progression.Session{Date: sum.CompletedAt, Plan: se.ThisSession.Plan, Done: se.ThisSession.Done})
		hist := sessions[max(0, len(sessions)-2):]
		want, err := progression.Next(squatCfg, hist, progression.ColdStart{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*se.Next, want) {
			t.Fatalf("step %d: summary next %+v\nwant progression.Next %+v", i, *se.Next, want)
		}
		if se.Next.Phase != st.phase || se.Next.Sets[0].WeightG != st.set1 || !strings.Contains(se.Next.Summary, st.why) {
			t.Fatalf("step %d: next = %s %d g %q; want %s %d g %q", i, se.Next.Phase, se.Next.Sets[0].WeightG, se.Next.Summary, st.phase, st.set1, st.why)
		}
		nextPlan = se.Next

		e.finishDay(t, c) // push
		e.finishDay(t, c) // pull
	}
}

func exercisePreview(t *testing.T, p workout.Preview, id string) workout.Exercise {
	t.Helper()
	for _, ex := range p.Exercises {
		if ex.ID == id {
			return ex
		}
	}
	t.Fatalf("exercise %s not in preview", id)
	return workout.Exercise{}
}
