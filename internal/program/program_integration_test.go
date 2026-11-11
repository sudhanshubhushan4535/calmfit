package program_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/program"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
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

func (e *env) login(t *testing.T, name string) *http.Client {
	return testutil.Login(t, e.url, e.fv, e.prefix+name)
}

func (e *env) onboard(t *testing.T, c *http.Client, body string) (int, string) {
	t.Helper()
	resp, err := c.Post(e.url+"/onboarding", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func (e *env) next(t *testing.T, c *http.Client) (int, string) {
	t.Helper()
	resp, err := c.Get(e.url + "/workouts/next")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func mustOnboard(t *testing.T, e *env, c *http.Client, days int) program.Program {
	t.Helper()
	status, body := e.onboard(t, c, `{"days_per_week":`+itoa(days)+`}`)
	if status != http.StatusOK {
		t.Fatalf("onboard(%d): status %d body %s", days, status, body)
	}
	var p program.Program
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode program: %v (%s)", err, body)
	}
	return p
}

func mustNext(t *testing.T, e *env, c *http.Client) workout.Preview {
	t.Helper()
	status, body := e.next(t, c)
	if status != http.StatusOK {
		t.Fatalf("next: status %d body %s", status, body)
	}
	var w workout.Preview
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatalf("decode workout: %v (%s)", err, body)
	}
	return w
}

// setLastCompleted moves the rotation directly, so these tests don't depend on the workout flow.
func setLastCompleted(t *testing.T, e *env, name string, idx int) {
	t.Helper()
	tag, err := e.db.Exec(context.Background(), `
		UPDATE user_programs up SET last_completed_index = $2
		FROM users u WHERE up.user_id = u.id AND u.google_sub = $1`, e.prefix+name, idx)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("set last_completed_index for %s: rows=%d err=%v", name, tag.RowsAffected(), err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func ids(w workout.Preview) []string {
	out := make([]string, len(w.Exercises))
	for i, ex := range w.Exercises {
		out[i] = ex.ID
	}
	return out
}

func TestUnauthenticatedIs401(t *testing.T) {
	e := setup(t)
	anon := &http.Client{}
	if status, _ := e.onboard(t, anon, `{"days_per_week":3}`); status != http.StatusUnauthorized {
		t.Fatalf("POST /onboarding without session: %d, want 401", status)
	}
	if status, _ := e.next(t, anon); status != http.StatusUnauthorized {
		t.Fatalf("GET /workouts/next without session: %d, want 401", status)
	}
}

func TestOnboardingRejectsInvalidDays(t *testing.T) {
	e := setup(t)
	c := e.login(t, "invalid")
	for _, body := range []string{
		`{"days_per_week":1}`,
		`{"days_per_week":7}`,
		`{"days_per_week":0}`,
		`{"days_per_week":-3}`,
		`{"days_per_week":3.5}`,
		`{"days_per_week":"3"}`,
		`{"days_per_week":null}`,
		`{}`,
		`not json`,
	} {
		t.Run(body, func(t *testing.T) {
			if status, resp := e.onboard(t, c, body); status != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (body %s)", status, resp)
			}
		})
	}

	resp, err := c.Post(e.url+"/onboarding", "application/x-www-form-urlencoded", strings.NewReader("days_per_week=3"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: %d, want 415", resp.StatusCode)
	}

	// None of the rejected requests may have created a program.
	if status, _ := e.next(t, c); status != http.StatusConflict {
		t.Fatalf("after only invalid onboarding, next = %d, want 409", status)
	}
}

func TestNextBeforeOnboardingIs409(t *testing.T) {
	e := setup(t)
	status, body := e.next(t, e.login(t, "fresh"))
	if status != http.StatusConflict || body != `{"error":"onboarding_required"}` {
		t.Fatalf("got %d %s, want 409 {\"error\":\"onboarding_required\"}", status, body)
	}
}

func TestNextAfterOnboardingIsPush(t *testing.T) {
	e := setup(t)
	c := e.login(t, "push")
	p := mustOnboard(t, e, c, 3)
	if p.TemplateID != "ppl" || p.DaysPerWeek != 3 || p.LastCompletedIndex != -1 {
		t.Fatalf("program = %+v, want ppl / 3 days / -1", p)
	}

	w := mustNext(t, e, c)
	want := []string{"Incline_Dumbbell_Press", "Butterfly", "Dumbbell_Shoulder_Press", "Side_Lateral_Raise", "Triceps_Pushdown"}
	if w.Day != "push" || w.DayIndex != 0 || !slices.Equal(ids(w), want) {
		t.Fatalf("next = %s/%d %v, want push/0 %v", w.Day, w.DayIndex, ids(w), want)
	}
	for _, ex := range w.Exercises {
		if !slices.Equal(ex.RepTargets, []int{12, 10, 8}) || ex.Name == "" || ex.TargetMuscle == "" || ex.MovementType == "" {
			t.Fatalf("exercise %+v: want filled fields and rep targets [12 10 8]", ex)
		}
		// No history yet: every exercise asks for the usual weight instead of previewing one.
		if !ex.NeedsUsualWeight || ex.Plan != nil || ex.WeightStepG <= 0 {
			t.Fatalf("exercise %s: needs_usual_weight=%v plan=%v step=%d, want true/nil/>0", ex.ID, ex.NeedsUsualWeight, ex.Plan, ex.WeightStepG)
		}
	}
	if w.Exercises[0].TargetMuscle != "upper_chest" || w.Exercises[0].MovementType != "compound" {
		t.Fatalf("first exercise = %+v, want upper_chest compound", w.Exercises[0])
	}
}

func TestReonboardingKeepsProgress(t *testing.T) {
	e := setup(t)
	c := e.login(t, "reonboard")
	mustOnboard(t, e, c, 3)
	setLastCompleted(t, e, "reonboard", 1) // push and pull done

	p := mustOnboard(t, e, c, 5)
	if p.DaysPerWeek != 5 || p.LastCompletedIndex != 1 {
		t.Fatalf("after re-onboarding: %+v, want days 5 and last_completed_index still 1", p)
	}

	w := mustNext(t, e, c)
	if w.Day != "legs" || w.DayIndex != 2 || len(w.Exercises) != 5 {
		t.Fatalf("next = %s/%d with %d exercises, want legs/2 with 5", w.Day, w.DayIndex, len(w.Exercises))
	}
	calf := w.Exercises[4]
	if calf.ID != "Seated_Calf_Raise" || calf.RepScheme != "high_rep" || !slices.Equal(calf.RepTargets, []int{18, 15, 12}) {
		t.Fatalf("last legs exercise = %+v, want Seated_Calf_Raise high_rep [18 15 12]", calf)
	}
}

func TestUsersAreIsolated(t *testing.T) {
	e := setup(t)
	a := e.login(t, "a")
	b := e.login(t, "b")

	mustOnboard(t, e, a, 4)
	setLastCompleted(t, e, "a", 0) // A finished push
	if w := mustNext(t, e, a); w.Day != "pull" {
		t.Fatalf("A next = %s, want pull", w.Day)
	}

	if status, _ := e.next(t, b); status != http.StatusConflict {
		t.Fatalf("B before onboarding = %d, want 409 despite A's program", status)
	}
	if p := mustOnboard(t, e, b, 2); p.LastCompletedIndex != -1 || p.DaysPerWeek != 2 {
		t.Fatalf("B program = %+v, want fresh -1 / 2 days", p)
	}
	if w := mustNext(t, e, b); w.Day != "push" {
		t.Fatalf("B next = %s, want push", w.Day)
	}
	if w := mustNext(t, e, a); w.Day != "pull" {
		t.Fatalf("A next after B onboarded = %s, want still pull", w.Day)
	}
}
