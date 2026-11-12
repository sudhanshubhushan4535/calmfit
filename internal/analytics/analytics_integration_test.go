package analytics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

func setup(t *testing.T) (*pgxpool.Pool, string, *testutil.FakeVerifier, string) {
	t.Helper()
	db := testutil.DB(t)
	prefix := testutil.SubPrefix(t, db)
	fv := testutil.NewFakeVerifier()
	srv := httptest.NewServer(server.New(db, fv, "test-client-id", testutil.Config(t)))
	t.Cleanup(srv.Close)
	return db, srv.URL, fv, prefix
}

func post(t *testing.T, c *http.Client, url, body string) int {
	t.Helper()
	resp, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// names returns the user's events in order.
func names(t *testing.T, db *pgxpool.Pool, sub string) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), `
		SELECT e.name FROM analytics_events e JOIN users u ON u.id = e.user_id
		WHERE u.google_sub = $1 ORDER BY e.id`, sub)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

func TestEventsFromAppFlow(t *testing.T) {
	db, url, fv, prefix := setup(t)
	sub := prefix + "a"
	c := testutil.Login(t, url, fv, sub)
	testutil.Login(t, url, fv, sub) // signing in again is not a signup
	testutil.Do(t, c, "POST", url+"/onboarding", `{"days_per_week":3}`)

	// One workout per day of the rotation: push, pull, legs.
	var first string
	for _, ex := range []string{"Side_Lateral_Raise", "Hammer_Curls", "Barbell_Squat"} {
		var w struct{ ID string }
		if err := json.Unmarshal(testutil.Do(t, c, "POST", url+"/workouts/start", ""), &w); err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = w.ID
		}
		testutil.Do(t, c, "POST", url+"/workouts/"+w.ID+"/exercises/"+ex+"/cold-start", `{"dont_know":true}`)
		testutil.Do(t, c, "POST", url+"/workouts/"+w.ID+"/sets", `{"exercise_id":"`+ex+`","set_number":1,"weight_g":2500,"reps":12}`)
		testutil.Do(t, c, "POST", url+"/workouts/"+w.ID+"/complete", "")
	}
	testutil.Do(t, c, "POST", url+"/workouts/"+first+"/complete", "") // repeat: no new events

	today := time.Now().In(testutil.Config(t).Location).Format(time.DateOnly)
	testutil.Do(t, c, "PUT", url+"/plate/"+today+"/lunch", `{"protein":true,"fiber":false}`)

	if code := post(t, c, url+"/events", `{"name":"next_target_viewed"}`); code != http.StatusNoContent {
		t.Fatalf("next_target_viewed = %d", code)
	}
	// The client may post only next_target_viewed; everything else is server-side.
	for _, body := range []string{`{"name":"signup_completed"}`, `{"name":"workout_completed"}`, `{"name":"x"}`, `{"name":"next_target_viewed","extra":1}`} {
		if code := post(t, c, url+"/events", body); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, code)
		}
	}
	if code := post(t, &http.Client{}, url+"/events", `{"name":"next_target_viewed"}`); code != http.StatusUnauthorized {
		t.Errorf("without a session = %d, want 401", code)
	}

	want := "signup_completed," +
		"first_workout_completed,workout_completed," +
		"second_workout_completed,workout_completed," +
		"workout_completed," +
		"plate_checked,next_target_viewed"
	if got := strings.Join(names(t, db, sub), ","); got != want {
		t.Fatalf("events:\n got %s\nwant %s", got, want)
	}
}

// query is one named query from docs/PILOT_METRICS.sql with psql variables turned into parameters.
type query struct{ name, sql string }

var psqlVar = regexp.MustCompile(`:'(pilot_start|pilot_end|as_of)'`)

func pilotQueries(t *testing.T) []query {
	t.Helper()
	b, err := os.ReadFile("../../docs/PILOT_METRICS.sql")
	if err != nil {
		t.Fatal(err)
	}
	var qs []query
	for _, part := range strings.Split(string(b), "-- name: ")[1:] {
		name, body, _ := strings.Cut(part, "\n")
		body = psqlVar.ReplaceAllStringFunc(body, func(m string) string {
			return map[string]string{":'pilot_start'": "$1", ":'pilot_end'": "$2", ":'as_of'": "$3"}[m]
		})
		qs = append(qs, query{strings.TrimSpace(name), strings.TrimSuffix(strings.TrimSpace(body), ";")})
	}
	if len(qs) != 4 {
		t.Fatalf("found %d queries in PILOT_METRICS.sql, want 4", len(qs))
	}
	return qs
}

type row struct {
	users, outOf int
	pct          *float64
}

func runPilot(t *testing.T, db *pgxpool.Pool, start, end, asOf string) map[string]row {
	t.Helper()
	got := map[string]row{}
	for _, q := range pilotQueries(t) {
		var metric, target string
		var r row
		if err := db.QueryRow(context.Background(), q.sql, start, end, asOf).Scan(&metric, &r.users, &r.outOf, &r.pct, &target); err != nil {
			t.Fatalf("%s: %v", q.name, err)
		}
		if metric != q.name {
			t.Fatalf("query %s reports metric %s", q.name, metric)
		}
		got[metric] = r
	}
	return got
}

// A hand-built cohort in 2030 (no other test writes events then), with every edge on a boundary
// the spec defines. Expected numbers are worked out in the comments.
func TestPilotMetrics(t *testing.T) {
	db, _, _, prefix := setup(t)
	ctx := context.Background()
	ts := func(s string) time.Time {
		v, err := time.Parse("2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	user := func(name string, evs ...[2]string) {
		var uid string
		if err := db.QueryRow(ctx, `INSERT INTO users (google_sub, email) VALUES ($1, $1 || '@example.com') RETURNING id::text`,
			prefix+name).Scan(&uid); err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			if _, err := db.Exec(ctx, `INSERT INTO analytics_events (user_id, name, occurred_at) VALUES ($1::uuid, $2, $3)`,
				uid, ev[0], ts(ev[1])); err != nil {
				t.Fatal(err)
			}
		}
	}
	first := func(at string) [][2]string {
		return [][2]string{{"first_workout_completed", at}, {"workout_completed", at}}
	}
	second := func(at string) [][2]string {
		return [][2]string{{"second_workout_completed", at}, {"workout_completed", at}}
	}
	cat := func(parts ...[][2]string) [][2]string {
		var out [][2]string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	// u1: activated (10 h), second after 3 days, week 2 (day 10), week 4 (day 24).
	user("u1", cat([][2]string{{"signup_completed", "2030-01-01 10:00"}}, first("2030-01-01 20:00"), second("2030-01-04 10:00"),
		[][2]string{{"workout_completed", "2030-01-10 10:00"}, {"workout_completed", "2030-01-24 10:00"}})...)
	// u2: activated, nothing after.
	user("u2", cat([][2]string{{"signup_completed", "2030-01-02 09:00"}}, first("2030-01-02 14:00"))...)
	// u3: first workout after exactly 48 h (not activated), but active in week 4.
	user("u3", cat([][2]string{{"signup_completed", "2030-01-03 08:00"}}, first("2030-01-05 08:00"), second("2030-01-12 08:00"),
		[][2]string{{"workout_completed", "2030-01-26 08:00"}})...)
	// u4: signed up, never trained.
	user("u4", [2]string{"signup_completed", "2030-01-03 09:00"})
	// u5: signed up before the pilot window: excluded from everything.
	user("u5", cat([][2]string{{"signup_completed", "2029-12-31 10:00"}}, first("2029-12-31 12:00"),
		[][2]string{{"workout_completed", "2030-01-25 10:00"}})...)
	// u6: activated; second workout 8 days after the first (too late for "second within 7 days"),
	// which is day 9 after signup, so it counts for week 2.
	user("u6", cat([][2]string{{"signup_completed", "2030-01-04 07:00"}}, first("2030-01-04 09:00"), second("2030-01-12 10:00"))...)

	got := runPilot(t, db, "2030-01-01", "2030-01-08", "2030-03-01")
	pct := func(v float64) *float64 { return &v }
	want := map[string]row{
		"first_workout_activation": {3, 5, pct(60.0)}, // u1 u2 u6 of u1 u2 u3 u4 u6
		"second_workout":           {1, 3, pct(33.3)}, // u1 of u1 u2 u6
		"week2_retention":          {2, 3, pct(66.7)}, // u1 u6 of u1 u2 u6
		"week4_active":             {2, 5, pct(40.0)}, // u1 u3 (activation not required)
	}
	for m, w := range want {
		if g := got[m]; g.users != w.users || g.outOf != w.outOf || g.pct == nil || *g.pct != *w.pct {
			t.Errorf("%s = %d/%d (%v%%), want %d/%d (%v%%)", m, g.users, g.outOf, deref(g.pct), w.users, w.outOf, *w.pct)
		}
	}

	// as_of = 2030-01-04 08:00: only users whose 24 h window has fully ended count (u1, u2, and u3
	// exactly at its end); no week has ended yet, so those report 0 of 0 with no percentage.
	early := runPilot(t, db, "2030-01-01", "2030-01-08", "2030-01-04 08:00")
	if a := early["first_workout_activation"]; a.users != 2 || a.outOf != 3 {
		t.Errorf("early activation = %d/%d, want 2/3", a.users, a.outOf)
	}
	for _, m := range []string{"second_workout", "week2_retention", "week4_active"} {
		if r := early[m]; r.outOf != 0 || r.pct != nil {
			t.Errorf("early %s = %d/%d (%v), want 0/0 and no pct", m, r.users, r.outOf, deref(r.pct))
		}
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
