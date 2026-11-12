package account_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/account"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

type env struct {
	db     *pgxpool.Pool
	url    string
	fv     *testutil.FakeVerifier
	prefix string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.DB(t)
	prefix := testutil.SubPrefix(t, db)
	fv := testutil.NewFakeVerifier()
	srv := httptest.NewServer(server.New(db, fv, "test-client-id", testutil.Config(t)))
	t.Cleanup(srv.Close)
	return &env{db: db, url: srv.URL, fv: fv, prefix: prefix}
}

func (e *env) uid(t *testing.T, sub string) string {
	t.Helper()
	var id string
	if err := e.db.QueryRow(context.Background(), `SELECT id::text FROM users WHERE google_sub = $1`, e.prefix+sub).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// A deleted user's tombstone has no google_sub, so SubPrefix can't find it: clean up by id.
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = e.db.Exec(ctx, `DELETE FROM outbox WHERE user_id = $1::uuid`, id)
		_, _ = e.db.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, id)
	})
	return id
}

// populate gives the signed-in user a row in every user table: program, a completed workout with
// sets, a plate check and a weekly_stats row. Returns the workout id.
func (e *env) populate(t *testing.T, c *http.Client, uid string) string {
	t.Helper()
	testutil.Do(t, c, "POST", e.url+"/onboarding", `{"days_per_week":3}`)
	var w struct{ ID string }
	if err := json.Unmarshal(testutil.Do(t, c, "POST", e.url+"/workouts/start", ""), &w); err != nil {
		t.Fatal(err)
	}
	testutil.Do(t, c, "POST", e.url+"/workouts/"+w.ID+"/exercises/Side_Lateral_Raise/cold-start", `{"usual_kg":5}`)
	testutil.Do(t, c, "POST", e.url+"/workouts/"+w.ID+"/sets", `{"exercise_id":"Side_Lateral_Raise","set_number":1,"weight_g":5000,"reps":12}`)
	testutil.Do(t, c, "POST", e.url+"/workouts/"+w.ID+"/complete", "")
	testutil.Do(t, c, "PUT", e.url+"/plate/"+time.Now().In(testutil.Config(t).Location).Format(time.DateOnly)+"/lunch", `{"protein":true,"fiber":false}`)
	if _, err := e.db.Exec(context.Background(), `
		INSERT INTO weekly_stats (user_id, week_start, workouts, sets, volume_g) VALUES ($1::uuid, '2026-11-09', 1, 1, 60000)`, uid); err != nil {
		t.Fatal(err)
	}
	// Push rows go straight into the DB: the subscribe route only exists with a VAPID key.
	if _, err := e.db.Exec(context.Background(), `
		INSERT INTO push_subscriptions (endpoint, user_id, p256dh, auth) VALUES ($1, $2::uuid, 'k', 'a')`,
		"https://fcm.googleapis.com/fcm/send/"+uid, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(context.Background(), `
		INSERT INTO reminder_log (user_id, local_date) VALUES ($1::uuid, '2026-11-10')`, uid); err != nil {
		t.Fatal(err)
	}
	return w.ID
}

func status(t *testing.T, c *http.Client, method, url string) (int, *http.Response) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp
}

func (e *env) count(t *testing.T, table, uid string) int {
	t.Helper()
	q := map[string]string{
		"workout_exercises": `SELECT count(*) FROM workout_exercises we JOIN workouts w ON w.id = we.workout_id WHERE w.user_id = $1::uuid`,
		"workout_sets":      `SELECT count(*) FROM workout_sets s JOIN workouts w ON w.id = s.workout_id WHERE w.user_id = $1::uuid`,
	}[table]
	if q == "" {
		q = `SELECT count(*) FROM ` + pgx.Identifier{table}.Sanitize() + ` WHERE user_id = $1::uuid`
	}
	var n int
	if err := e.db.QueryRow(context.Background(), q, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var ownedTables = []string{"sessions", "user_programs", "workouts", "workout_exercises", "workout_sets", "plate_checks", "weekly_stats",
	"push_subscriptions", "reminder_log"}

func TestDeleteAccount(t *testing.T) {
	e := setup(t)
	a := testutil.Login(t, e.url, e.fv, e.prefix+"a")
	a2 := testutil.Login(t, e.url, e.fv, e.prefix+"a") // a second device
	b := testutil.Login(t, e.url, e.fv, e.prefix+"b")
	uidA, uidB := e.uid(t, "a"), e.uid(t, "b")
	e.populate(t, a, uidA)
	e.populate(t, b, uidB)
	for _, tbl := range ownedTables {
		if e.count(t, tbl, uidA) == 0 {
			t.Fatalf("setup: user A has no %s rows", tbl)
		}
	}

	code, resp := status(t, a, "DELETE", e.url+"/me")
	if code != http.StatusNoContent {
		t.Fatalf("DELETE /me = %d, want 204", code)
	}
	if c := resp.Cookies(); len(c) != 1 || c[0].Name != "calmfit_session" || c[0].MaxAge >= 0 {
		t.Fatalf("delete did not clear the cookie: %v", c)
	}

	// Every session is revoked, not just the one used.
	for name, c := range map[string]*http.Client{"same device": a, "other device": a2} {
		if code, _ := status(t, c, "GET", e.url+"/me"); code != http.StatusUnauthorized {
			t.Errorf("%s after delete: GET /me = %d, want 401", name, code)
		}
	}
	if code, _ := status(t, a2, "DELETE", e.url+"/me"); code != http.StatusUnauthorized {
		t.Errorf("second delete = %d, want 401", code)
	}

	for _, tbl := range ownedTables {
		if n := e.count(t, tbl, uidA); n != 0 {
			t.Errorf("%s still has %d rows for the deleted user", tbl, n)
		}
		if e.count(t, tbl, uidB) == 0 {
			t.Errorf("user B lost %s rows", tbl)
		}
	}

	var deleted bool
	var sub, name *string
	var email string
	if err := e.db.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL, google_sub, email, name FROM users WHERE id = $1::uuid`, uidA).Scan(&deleted, &sub, &email, &name); err != nil {
		t.Fatal(err)
	}
	if !deleted || sub != nil || email != "" || name != nil {
		t.Fatalf("tombstone: deleted=%v sub=%v email=%q name=%v", deleted, sub, email, name)
	}

	// Earlier events are gone; user.deleted, written in the same transaction, is the only one left.
	rows, err := e.db.Query(context.Background(), `SELECT type FROM outbox WHERE user_id = $1::uuid`, uidA)
	if err != nil {
		t.Fatal(err)
	}
	types, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 || types[0] != "user.deleted" {
		t.Fatalf("outbox for deleted user = %v, want [user.deleted]", types)
	}

	// The same Google account can sign up again, as a brand-new user with no old data.
	again := testutil.Login(t, e.url, e.fv, e.prefix+"a")
	newID := e.uid(t, "a")
	if newID == uidA {
		t.Fatal("re-signup reused the deleted user's id")
	}
	var x account.Export
	if err := json.Unmarshal(testutil.Do(t, again, "GET", e.url+"/me/export", ""), &x); err != nil {
		t.Fatal(err)
	}
	if string(x.Workouts) != "[]" || string(x.Program) != "null" || string(x.PlateChecks) != "[]" {
		t.Fatalf("new account has old data: %+v", x)
	}
}

func TestExportContainsOnlyOwnData(t *testing.T) {
	e := setup(t)
	a := testutil.Login(t, e.url, e.fv, e.prefix+"a")
	b := testutil.Login(t, e.url, e.fv, e.prefix+"b")
	uidA, uidB := e.uid(t, "a"), e.uid(t, "b")
	widA := e.populate(t, a, uidA)
	widB := e.populate(t, b, uidB)

	req, _ := http.NewRequest("GET", e.url+"/me/export", nil)
	resp, err := a.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") ||
		resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("export: %d %v", resp.StatusCode, resp.Header)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "id_hash") || strings.Contains(body, widB) || strings.Contains(body, uidB) {
		t.Fatal("export leaks the session hash or another user's data")
	}

	var x struct {
		User struct {
			ID, Email string
		}
		Program *struct {
			DaysPerWeek int `json:"days_per_week"`
		}
		Workouts []struct {
			ID        string
			Status    string
			Exercises []struct {
				ExerciseID string `json:"exercise_id"`
				Plan       json.RawMessage
				Sets       []struct {
					WeightG int `json:"weight_g"`
					Reps    int
				}
			}
		}
		PlateChecks []struct{ Meal string } `json:"plate_checks"`
		WeeklyStats []struct {
			VolumeG int64 `json:"volume_g"`
		} `json:"weekly_stats"`
		Sessions []struct {
			CreatedAt time.Time `json:"created_at"`
		}
		PushSubscriptions []map[string]any `json:"push_subscriptions"`
		Reminders         []struct {
			LocalDate string `json:"local_date"`
		}
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		t.Fatal(err)
	}
	if x.User.ID != uidA || x.User.Email != e.prefix+"a@example.com" || x.Program == nil || x.Program.DaysPerWeek != 3 {
		t.Fatalf("user/program: %+v %+v", x.User, x.Program)
	}
	if len(x.Workouts) != 1 || x.Workouts[0].ID != widA || x.Workouts[0].Status != "completed" || len(x.Workouts[0].Exercises) != 5 {
		t.Fatalf("workouts: %+v", x.Workouts)
	}
	var logged int
	for _, ex := range x.Workouts[0].Exercises {
		for _, s := range ex.Sets {
			logged++
			if ex.ExerciseID != "Side_Lateral_Raise" || s.WeightG != 5000 || s.Reps != 12 || string(ex.Plan) == "null" {
				t.Fatalf("set: %s %+v plan=%s", ex.ExerciseID, s, ex.Plan)
			}
		}
	}
	if logged != 1 || len(x.PlateChecks) != 1 || len(x.WeeklyStats) != 1 || x.WeeklyStats[0].VolumeG != 60000 ||
		len(x.Sessions) != 1 || x.Sessions[0].CreatedAt.IsZero() {
		t.Fatalf("sets=%d plate=%d stats=%+v sessions=%+v", logged, len(x.PlateChecks), x.WeeklyStats, x.Sessions)
	}
	// Subscriptions are exported without their encryption keys.
	if len(x.PushSubscriptions) != 1 || x.PushSubscriptions[0]["endpoint"] != "https://fcm.googleapis.com/fcm/send/"+uidA ||
		x.PushSubscriptions[0]["p256dh"] != nil || x.PushSubscriptions[0]["auth"] != nil ||
		len(x.Reminders) != 1 || x.Reminders[0].LocalDate != "2026-11-10" {
		t.Fatalf("push=%v reminders=%+v", x.PushSubscriptions, x.Reminders)
	}
}

func TestExportRequiresSession(t *testing.T) {
	e := setup(t)
	for _, m := range []string{"GET /me/export", "DELETE /me"} {
		method, path, _ := strings.Cut(m, " ")
		if code, _ := status(t, &http.Client{}, method, e.url+path); code != http.StatusUnauthorized {
			t.Errorf("%s without a session = %d, want 401", m, code)
		}
	}
}

// Every table is classified as exported (user data), internal or reference, so a table added
// later can't be missed by the export or by deletion without this test failing.
func TestEveryTableIsClassified(t *testing.T) {
	db := testutil.DB(t)
	rows, err := db.Query(context.Background(), `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	lists := [][]string{account.ExportedTables, account.InternalTables, account.ReferenceTables}
	for _, tbl := range tables {
		n := 0
		for _, l := range lists {
			if slices.Contains(l, tbl) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("table %s is in %d of account.ExportedTables/InternalTables/ReferenceTables, want exactly 1", tbl, n)
		}
	}
	for _, l := range lists {
		for _, tbl := range l {
			if !slices.Contains(tables, tbl) {
				t.Errorf("%s is listed but doesn't exist", tbl)
			}
		}
	}
	// The deletion test checks every exported table except users (which becomes a tombstone).
	for _, tbl := range account.ExportedTables {
		if tbl != "users" && !slices.Contains(ownedTables, tbl) {
			t.Errorf("exported table %s is not checked by TestDeleteAccount", tbl)
		}
	}
}
