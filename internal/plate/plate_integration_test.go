package plate_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/plate"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

type env struct {
	url    string
	fv     *testutil.FakeVerifier
	prefix string
	today  time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.DB(t)
	cfg := testutil.Config(t)
	prefix := testutil.SubPrefix(t, db)
	fv := testutil.NewFakeVerifier()
	srv := httptest.NewServer(server.New(db, fv, "test-client-id", cfg))
	t.Cleanup(srv.Close)
	return &env{url: srv.URL, fv: fv, prefix: prefix, today: cfg.Today(time.Now())}
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

func (e *env) date(daysAgo int) string {
	return e.today.AddDate(0, 0, -daysAgo).Format(time.DateOnly)
}

func (e *env) put(t *testing.T, c *http.Client, daysAgo int, meal string, protein, fiber bool) plate.Day {
	t.Helper()
	body, _ := json.Marshal(map[string]bool{"protein": protein, "fiber": fiber})
	status, b := e.do(t, c, "PUT", "/plate/"+e.date(daysAgo)+"/"+meal, string(body))
	if status != http.StatusOK {
		t.Fatalf("put %d %s: %d %s", daysAgo, meal, status, b)
	}
	var d plate.Day
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func (e *env) summary(t *testing.T, c *http.Client, query string) plate.Summary {
	t.Helper()
	status, b := e.do(t, c, "GET", "/plate"+query, "")
	if status != http.StatusOK {
		t.Fatalf("get /plate%s: %d %s", query, status, b)
	}
	var s plate.Summary
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// goodDay logs a 6-point day (protein + fiber at breakfast and lunch).
func (e *env) goodDay(t *testing.T, c *http.Client, daysAgo int) {
	e.put(t, c, daysAgo, "breakfast", true, true)
	e.put(t, c, daysAgo, "lunch", true, true)
}

func TestPlateUpsertScoreAndStreak(t *testing.T) {
	e := setup(t)
	c := testutil.Login(t, e.url, e.fv, e.prefix+"plate")

	// Upsert is idempotent and an edit replaces the meal.
	e.put(t, c, 0, "breakfast", true, false)
	e.put(t, c, 0, "breakfast", true, false)
	if d := e.put(t, c, 0, "breakfast", true, true); d.Points != 3 || d.Good {
		t.Fatalf("after edit: %+v", d)
	}
	if d := e.put(t, c, 0, "lunch", true, true); d.Points != 6 || d.Percent != 66 || !d.Good || d.Max != 9 {
		t.Fatalf("good day: %+v", d)
	}

	// Yesterday and 2 days ago good, 3 days ago not, 4 days ago good.
	e.goodDay(t, c, 1)
	e.goodDay(t, c, 2)
	e.put(t, c, 3, "dinner", true, false)
	e.goodDay(t, c, 4)

	s := e.summary(t, c, "")
	if s.Streak != 3 || !s.Today.Good || s.GoodDayMin != 6 || len(s.Days) != 7 {
		t.Fatalf("summary: streak=%d today=%+v days=%d", s.Streak, s.Today, len(s.Days))
	}
	if last := s.Days[6]; last.Date != e.date(0) || last.Points != 6 {
		t.Fatalf("last day: %+v", last)
	}

	// Taking today back to not-good doesn't break the streak; it just stops counting today.
	e.put(t, c, 0, "lunch", false, false)
	if s := e.summary(t, c, ""); s.Streak != 2 || s.Today.Good {
		t.Fatalf("today unfinished: streak=%d", s.Streak)
	}

	// A custom range.
	s = e.summary(t, c, "?from="+e.date(4)+"&to="+e.date(3))
	if len(s.Days) != 2 || s.Days[0].Points != 6 || s.Days[1].Points != 2 {
		t.Fatalf("range: %+v", s.Days)
	}
}

// Days with no entries are neutral: they neither break nor extend the streak.
func TestPlateEmptyDaysAreNeutral(t *testing.T) {
	e := setup(t)
	c := testutil.Login(t, e.url, e.fv, e.prefix+"neutral")
	e.put(t, c, 6, "lunch", true, false) // logged, not good: the streak starts after this
	e.goodDay(t, c, 5)
	e.goodDay(t, c, 3) // day 4 empty
	e.goodDay(t, c, 1) // day 2 empty, today empty
	s := e.summary(t, c, "")
	if s.Streak != 3 {
		t.Fatalf("streak = %d, want 3", s.Streak)
	}
	if s.Days[2].Logged || !s.Days[1].Logged || s.Days[1].Points != 6 {
		t.Fatalf("logged flags: %+v", s.Days)
	}
}

func TestPlateIsUserScoped(t *testing.T) {
	e := setup(t)
	a := testutil.Login(t, e.url, e.fv, e.prefix+"a")
	b := testutil.Login(t, e.url, e.fv, e.prefix+"b")
	e.goodDay(t, a, 0)
	if s := e.summary(t, b, ""); s.Today.Points != 0 || s.Streak != 0 {
		t.Fatalf("user b sees a's checks: %+v", s.Today)
	}
}

func TestPlateValidation(t *testing.T) {
	e := setup(t)
	c := testutil.Login(t, e.url, e.fv, e.prefix+"v")
	ok := `{"protein":true,"fiber":false}`
	for name, tc := range map[string]struct{ path, body string }{
		"future date":   {"/plate/" + e.date(-1) + "/lunch", ok},
		"too old":       {"/plate/" + e.date(plate.EditableDays+1) + "/lunch", ok},
		"bad date":      {"/plate/2026-13-01/lunch", ok},
		"bad meal":      {"/plate/" + e.date(0) + "/snack", ok},
		"missing field": {"/plate/" + e.date(0) + "/lunch", `{"protein":true}`},
		"unknown field": {"/plate/" + e.date(0) + "/lunch", `{"protein":true,"fiber":true,"x":1}`},
	} {
		if status, b := e.do(t, c, "PUT", tc.path, tc.body); status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, status, b)
		}
	}
	// Oldest editable day is accepted.
	e.put(t, c, plate.EditableDays, "dinner", true, true)

	for name, q := range map[string]string{
		"future to":   "?to=" + e.date(-1),
		"from > to":   "?from=" + e.date(0) + "&to=" + e.date(1),
		"range > max": "?from=" + e.date(plate.MaxRangeDays) + "&to=" + e.date(0),
	} {
		if status, b := e.do(t, c, "GET", "/plate"+q, ""); status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, status, b)
		}
	}
	if status, _ := e.do(t, http.DefaultClient, "GET", "/plate", ""); status != http.StatusUnauthorized {
		t.Errorf("unauthenticated: %d, want 401", status)
	}
}
