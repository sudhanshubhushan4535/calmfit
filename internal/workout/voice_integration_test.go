package workout_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/ratelimit"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/voice"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

func kgp(v float64) *float64 { return &v }

// Transcripts the fake parser knows. Anything else is could_not_parse.
func fakeParser() *voice.FakeParser {
	return &voice.FakeParser{Results: map[string]voice.Parsed{
		"incline 20, 12 10 8": {ExerciseID: inclinePress, Unit: voice.KG,
			Sets: []voice.ParsedSet{{Weight: kgp(20), Reps: 12}, {Reps: 10}, {Reps: 8}}},
		"12 10 8":      {Sets: []voice.ParsedSet{{Reps: 12}, {Reps: 10}, {Reps: 8}}},
		"squat 60, 5":  {ExerciseID: squat, Unit: voice.KG, Sets: []voice.ParsedSet{{Weight: kgp(60), Reps: 5}}},
		"lateral 8, 9": {ExerciseID: lateral, Unit: voice.KG, Sets: []voice.ParsedSet{{Weight: kgp(8), Reps: 9}}},
		"lateral 8, 12 10 8": {ExerciseID: lateral, Unit: voice.KG,
			Sets: []voice.ParsedSet{{Weight: kgp(8), Reps: 12}, {Reps: 10}, {Reps: 8}}},
		"lateral 12 10": {ExerciseID: lateral, Sets: []voice.ParsedSet{{Reps: 12}, {Reps: 10}}},
		// What the API parser returns for an exercise outside today's list (its enum can't name it).
		"leg press 100, 10": {OtherExercise: true, Unit: voice.KG, Sets: []voice.ParsedSet{{Weight: kgp(100), Reps: 10}}},
	}}
}

// setupVoice is setup with the voice flag on, the given parser and a limiter: lim nil means the
// real Redis limiter with the repo's daily limit, under a per-test key prefix.
func setupVoice(t *testing.T, p voice.Parser, lim workout.Limiter) *env {
	t.Helper()
	db := testutil.DB(t)
	prefix := testutil.SubPrefix(t, db)
	fv := testutil.NewFakeVerifier()
	cfg := testutil.Config(t)
	cfg.Voice.Enabled = true
	if lim == nil {
		rdb, rp := testutil.Redis(t)
		lim = ratelimit.NewDaily(rdb, rp+":voice", cfg.Voice.DailyLimit, cfg)
	}
	srv := httptest.NewServer(server.New(db, fv, "test-client-id", cfg, server.WithVoice(p, lim)))
	t.Cleanup(srv.Close)
	return &env{url: srv.URL, db: db, fv: fv, prefix: prefix}
}

func (e *env) voiceParse(t *testing.T, c *http.Client, wid, transcript, hint string) (int, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"transcript": transcript, "exercise_id": hint})
	return e.do(t, c, "POST", "/workouts/"+wid+"/voice/parse", string(body))
}

func (e *env) mustVoiceParse(t *testing.T, c *http.Client, wid, transcript, hint string) workout.VoiceParse {
	t.Helper()
	status, b := e.voiceParse(t, c, wid, transcript, hint)
	if status != http.StatusOK {
		t.Fatalf("voice parse %q: %d %s", transcript, status, b)
	}
	return decode[workout.VoiceParse](t, b)
}

func errCode(t *testing.T, b []byte) string {
	t.Helper()
	return decode[map[string]string](t, b)["error"]
}

// snapshot is every business and outbox row the user owns, as text, so any write shows up.
func (e *env) snapshot(t *testing.T, sub string) string {
	t.Helper()
	var s string
	err := e.db.QueryRow(context.Background(), `
		WITH u AS (SELECT id FROM users WHERE google_sub = $1)
		SELECT concat_ws(' | ',
		  (SELECT string_agg(row_to_json(w)::text, ',' ORDER BY w.id) FROM workouts w WHERE w.user_id IN (SELECT id FROM u)),
		  (SELECT string_agg(row_to_json(we)::text, ',' ORDER BY we.workout_id, we.exercise_id)
		     FROM workout_exercises we JOIN workouts w ON w.id = we.workout_id WHERE w.user_id IN (SELECT id FROM u)),
		  (SELECT string_agg(row_to_json(ws)::text, ',' ORDER BY ws.workout_id, ws.exercise_id, ws.set_number)
		     FROM workout_sets ws JOIN workouts w ON w.id = ws.workout_id WHERE w.user_id IN (SELECT id FROM u)),
		  (SELECT string_agg(row_to_json(o)::text, ',' ORDER BY o.id) FROM outbox o WHERE o.user_id IN (SELECT id FROM u)),
		  (SELECT string_agg(row_to_json(up)::text, ',') FROM user_programs up WHERE up.user_id IN (SELECT id FROM u)))`,
		sub).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVoiceRouteIsOffUnlessEnabled(t *testing.T) {
	// The repo config ships voice off: even with a parser supplied, the route doesn't exist.
	db := testutil.DB(t)
	prefix := testutil.SubPrefix(t, db)
	fv := testutil.NewFakeVerifier()
	cfg := testutil.Config(t)
	if cfg.Voice.Enabled {
		t.Fatal("repo config must ship with voice off (D36)")
	}
	srv := httptest.NewServer(server.New(db, fv, "test-client-id", cfg, server.WithVoice(fakeParser(), nil)))
	t.Cleanup(srv.Close)
	e := &env{url: srv.URL, db: db, fv: fv, prefix: prefix}
	c := e.user(t, "off")
	_, w := e.start(t, c)
	if status, b := e.voiceParse(t, c, w.ID, "incline 20, 12 10 8", ""); status != http.StatusNotFound {
		t.Fatalf("flag off: %d %s, want 404", status, b)
	}
	// The default setup (no WithVoice) has no route either.
	e2 := setup(t)
	c2 := e2.user(t, "nodeps")
	_, w2 := e2.start(t, c2)
	if status, _ := e2.voiceParse(t, c2, w2.ID, "x", ""); status != http.StatusNotFound {
		t.Fatalf("no voice deps: %d, want 404", status)
	}
}

func TestVoiceRequestErrors(t *testing.T) {
	fake := fakeParser()
	e := setupVoice(t, fake, nil)
	owner := e.user(t, "owner")
	other := e.user(t, "other")
	_, w := e.start(t, owner)

	if status, _ := e.voiceParse(t, &http.Client{}, w.ID, "incline 20, 12 10 8", ""); status != http.StatusUnauthorized {
		t.Errorf("no session: %d, want 401", status)
	}
	if status, b := e.voiceParse(t, other, w.ID, "incline 20, 12 10 8", ""); status != http.StatusNotFound || errCode(t, b) != "workout_not_found" {
		t.Errorf("another user's workout: %d %s, want 404", status, b)
	}
	if status, _ := e.voiceParse(t, owner, "not-a-uuid", "incline 20, 12 10 8", ""); status != http.StatusNotFound {
		t.Errorf("malformed id: %d, want 404", status)
	}
	for _, tr := range []string{"", "   ", strings.Repeat("a", 201)} {
		if status, _ := e.voiceParse(t, owner, w.ID, tr, ""); status != http.StatusBadRequest {
			t.Errorf("transcript of %d chars: %d, want 400", len(tr), status)
		}
	}
	if status, _ := e.do(t, owner, "POST", "/workouts/"+w.ID+"/voice/parse", `{"transcript":"x","user_id":"y"}`); status != http.StatusBadRequest {
		t.Errorf("unknown field: %d, want 400", status)
	}
	// None of the rejected requests reached the parser.
	if n := len(fake.Calls()); n != 0 {
		t.Errorf("parser calls = %d, want 0", n)
	}

	// A completed workout can't take voice sets.
	ex := e.mustColdStart(t, owner, w.ID, lateral, `{"usual_kg":5}`)
	e.mustLogSet(t, owner, w.ID, lateral, 1, ex.Plan.Sets[0].WeightG, 12)
	e.mustComplete(t, owner, w.ID)
	if status, b := e.voiceParse(t, owner, w.ID, "lateral 8, 9", ""); status != http.StatusConflict || errCode(t, b) != "workout_completed" {
		t.Errorf("completed workout: %d %s, want 409", status, b)
	}
}

func TestVoiceParseResults(t *testing.T) {
	fake := fakeParser()
	e := setupVoice(t, fake, nil)
	c := e.user(t, "res")
	_, w := e.start(t, c)
	incl := e.mustColdStart(t, c, w.ID, inclinePress, `{"usual_kg":20}`)

	// One weight said: it applies to every set (D31).
	got := e.mustVoiceParse(t, c, w.ID, "incline 20, 12 10 8", "")
	want := []voice.Set{{SetNumber: 1, WeightG: 20000, Reps: 12, WeightSource: "said"},
		{SetNumber: 2, WeightG: 20000, Reps: 10, WeightSource: "said"},
		{SetNumber: 3, WeightG: 20000, Reps: 8, WeightSource: "said"}}
	if got.ExerciseID != inclinePress || got.ExerciseName != "Incline Dumbbell Press" || !reflect.DeepEqual(got.Sets, want) {
		t.Errorf("incline 20, 12 10 8 → %+v", got)
	}

	// No exercise or weight said: the open exercise and the plan's targets.
	got = e.mustVoiceParse(t, c, w.ID, "12 10 8", inclinePress)
	for i, s := range got.Sets {
		if s.SetNumber != i+1 || s.WeightG != incl.Plan.Sets[i].WeightG || s.WeightSource != "plan" {
			t.Errorf("set %d = %+v, want plan weight %d", i+1, s, incl.Plan.Sets[i].WeightG)
		}
	}
	if len(got.Warnings) != 1 {
		t.Errorf("warnings = %v, want the open-exercise warning", got.Warnings)
	}

	// The parser saw today's exercises with their aliases, and only the trimmed transcript.
	calls := fake.Calls()
	if len(calls) != 2 || calls[0].Transcript != "incline 20, 12 10 8" {
		t.Fatalf("calls = %+v", calls)
	}
	var sawAlias bool
	for _, cand := range calls[0].Candidates {
		sawAlias = sawAlias || (cand.ID == inclinePress && reflect.DeepEqual(cand.Aliases, []string{"incline dumbell"}))
	}
	if !sawAlias || len(calls[0].Candidates) != len(w.Exercises) {
		t.Errorf("candidates = %+v", calls[0].Candidates)
	}

	for _, tc := range []struct{ transcript, hint, code string }{
		{"hello there", "", voice.CodeCouldNotParse},
		{"squat 60, 5", "", voice.CodeNotInWorkout},       // legs exercise on push day
		{"lateral 12 10", "", voice.CodeNeedsUsualWeight}, // first time and no weight said
		{"12 10 8", "", voice.CodeNoExercise},
		{"leg press 100, 10", inclinePress, voice.CodeNotInWorkout}, // the open exercise must not stand in
	} {
		status, b := e.voiceParse(t, c, w.ID, tc.transcript, tc.hint)
		if status != http.StatusUnprocessableEntity || errCode(t, b) != tc.code {
			t.Errorf("%q: %d %s, want 422 %s", tc.transcript, status, b, tc.code)
		}
	}
}

func TestVoiceProviderFailureIs502(t *testing.T) {
	e := setupVoice(t, &voice.FakeParser{Err: fmt.Errorf("%w: upstream 529", voice.ErrProvider)}, nil)
	c := e.user(t, "p502")
	_, w := e.start(t, c)
	if status, b := e.voiceParse(t, c, w.ID, "incline 20, 12 10 8", ""); status != http.StatusBadGateway || errCode(t, b) != "voice_parser_unavailable" {
		t.Fatalf("provider failure: %d %s, want 502", status, b)
	}
}

// The parse endpoint must never write: every outcome leaves the user's rows byte-for-byte equal.
func TestVoiceParseNeverWrites(t *testing.T) {
	e := setupVoice(t, fakeParser(), nil)
	c := e.user(t, "ro")
	sub := e.prefix + "ro"
	_, w := e.start(t, c)
	e.mustColdStart(t, c, w.ID, inclinePress, `{"usual_kg":20}`)

	before := e.snapshot(t, sub)
	e.mustVoiceParse(t, c, w.ID, "incline 20, 12 10 8", "")
	e.mustVoiceParse(t, c, w.ID, "12 10 8", inclinePress)
	e.voiceParse(t, c, w.ID, "hello", "")
	e.voiceParse(t, c, w.ID, "squat 60, 5", "")
	if after := e.snapshot(t, sub); after != before {
		t.Fatalf("voice parse changed the user's rows:\nbefore %s\nafter  %s", before, after)
	}
	// The snapshot really sees writes: it holds the workout, and one tapped set changes it.
	if !strings.Contains(before, w.ID) {
		t.Fatalf("snapshot misses the workout: %s", before)
	}
	e.mustLogSet(t, c, w.ID, inclinePress, 1, 20000, 12)
	if e.snapshot(t, sub) == before {
		t.Fatal("snapshot didn't change after a logged set")
	}
}

// Voice reuses the tap path: confirmed sets go through POST /sets, and the workout.logged event
// is identical to one from a user who tapped the same sets.
func TestVoiceConfirmUsesTapPath(t *testing.T) {
	e := setupVoice(t, fakeParser(), nil)
	event := func(sub string) events.WorkoutLogged {
		t.Helper()
		var payload []byte
		if err := e.db.QueryRow(context.Background(), `
			SELECT o.payload FROM outbox o JOIN users u ON u.id = o.user_id
			WHERE u.google_sub = $1 AND o.type = $2`, sub, events.TypeWorkoutLogged).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		return decode[events.WorkoutLogged](t, payload)
	}

	// Voice user: parse, then log each confirmed set exactly as the test page does.
	vc := e.user(t, "voice")
	_, vw := e.start(t, vc)
	e.mustColdStart(t, vc, vw.ID, inclinePress, `{"usual_kg":20}`)
	p := e.mustVoiceParse(t, vc, vw.ID, "incline 20, 12 10 8", "")
	for _, s := range p.Sets {
		e.mustLogSet(t, vc, vw.ID, p.ExerciseID, s.SetNumber, s.WeightG, s.Reps)
	}
	e.mustComplete(t, vc, vw.ID)

	// Tap user: the same sets, tapped.
	tc := e.user(t, "tap")
	_, tw := e.start(t, tc)
	e.mustColdStart(t, tc, tw.ID, inclinePress, `{"usual_kg":20}`)
	for i, reps := range []int{12, 10, 8} {
		e.mustLogSet(t, tc, tw.ID, inclinePress, i+1, 20000, reps)
	}
	e.mustComplete(t, tc, tw.ID)

	ve, te := event(e.prefix+"voice"), event(e.prefix+"tap")
	if !reflect.DeepEqual(ve.Sets, te.Sets) || ve.Day != te.Day || len(ve.Sets) != 3 {
		t.Fatalf("voice event sets %+v differ from tap event sets %+v", ve.Sets, te.Sets)
	}
}

func TestVoiceDailyLimit(t *testing.T) {
	fake := fakeParser()
	e := setupVoice(t, fake, nil)
	c := e.user(t, "lim")
	_, w := e.start(t, c)
	e.mustColdStart(t, c, w.ID, inclinePress, `{"usual_kg":20}`)

	// Rejected requests (bad body, someone else's workout) don't use the allowance.
	for range 5 {
		e.voiceParse(t, c, w.ID, "", "")
		e.voiceParse(t, c, "00000000-0000-0000-0000-000000000000", "incline 20, 12 10 8", "")
	}
	limit := testutil.Config(t).Voice.DailyLimit
	for i := 1; i <= limit; i++ {
		// Every parse counts, whatever its outcome: alternate success and could_not_parse.
		tr := "incline 20, 12 10 8"
		if i%2 == 0 {
			tr = "hello"
		}
		if status, b := e.voiceParse(t, c, w.ID, tr, ""); status == http.StatusTooManyRequests {
			t.Fatalf("parse %d: 429 %s before the limit of %d", i, b, limit)
		}
	}
	calls := len(fake.Calls())
	if status, b := e.voiceParse(t, c, w.ID, "incline 20, 12 10 8", ""); status != http.StatusTooManyRequests || errCode(t, b) != "voice_daily_limit" {
		t.Fatalf("parse %d: %d %s, want 429", limit+1, status, b)
	}
	if n := len(fake.Calls()); n != calls {
		t.Errorf("a limited request reached the parser")
	}
	// Another user has their own allowance.
	c2 := e.user(t, "lim2")
	_, w2 := e.start(t, c2)
	e.mustColdStart(t, c2, w2.ID, inclinePress, `{"usual_kg":20}`)
	e.mustVoiceParse(t, c2, w2.ID, "incline 20, 12 10 8", "")
}

// Redis down: voice fails closed with 503, and tap logging is untouched.
func TestVoiceRedisDownFailsClosed(t *testing.T) {
	dead, err := ratelimit.NewClient("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dead.Close() })
	fake := fakeParser()
	e := setupVoice(t, fake, ratelimit.NewDaily(dead, "voice", 30, testutil.Config(t)))
	c := e.user(t, "down")
	_, w := e.start(t, c)
	ex := e.mustColdStart(t, c, w.ID, inclinePress, `{"usual_kg":20}`)

	if status, b := e.voiceParse(t, c, w.ID, "incline 20, 12 10 8", ""); status != http.StatusServiceUnavailable || errCode(t, b) != "voice_unavailable" {
		t.Fatalf("redis down: %d %s, want 503", status, b)
	}
	if n := len(fake.Calls()); n != 0 {
		t.Errorf("parser called %d times with the limiter down", n)
	}
	e.mustLogSet(t, c, w.ID, inclinePress, 1, ex.Plan.Sets[0].WeightG, 12)
	e.mustComplete(t, c, w.ID)
}

// A first-time exercise with set 1's weight said: the parse proposes it as the usual weight and
// writes nothing; confirming through the existing cold-start + set endpoints gives exactly what a
// tapping user who answered the question with that weight gets: same stored plan, same event.
func TestVoiceProposesUsualWeight(t *testing.T) {
	e := setupVoice(t, fakeParser(), nil)
	planOf := func(sub string) string {
		t.Helper()
		var plan string
		if err := e.db.QueryRow(context.Background(), `
			SELECT coalesce(we.plan::text, 'null') FROM workout_exercises we
			JOIN workouts w ON w.id = we.workout_id JOIN users u ON u.id = w.user_id
			WHERE u.google_sub = $1 AND we.exercise_id = $2`, sub, lateral).Scan(&plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	eventSets := func(sub string) []events.LoggedSet {
		t.Helper()
		var payload []byte
		if err := e.db.QueryRow(context.Background(), `
			SELECT o.payload FROM outbox o JOIN users u ON u.id = o.user_id
			WHERE u.google_sub = $1 AND o.type = $2`, sub, events.TypeWorkoutLogged).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		return decode[events.WorkoutLogged](t, payload).Sets
	}

	vc, vsub := e.user(t, "vfirst"), e.prefix+"vfirst"
	_, vw := e.start(t, vc)
	before := e.snapshot(t, vsub)
	p := e.mustVoiceParse(t, vc, vw.ID, "lateral 8, 12 10 8", "")
	if p.UsualWeightG == nil || *p.UsualWeightG != 8000 || p.ExerciseID != lateral {
		t.Fatalf("parse = %+v, want usual weight 8000 on %s", p, lateral)
	}
	wantSets := []voice.Set{{SetNumber: 1, WeightG: 8000, Reps: 12, WeightSource: "said"},
		{SetNumber: 2, WeightG: 8000, Reps: 10, WeightSource: "said"},
		{SetNumber: 3, WeightG: 8000, Reps: 8, WeightSource: "said"}}
	if !reflect.DeepEqual(p.Sets, wantSets) {
		t.Errorf("sets = %+v", p.Sets)
	}
	// Saved only on confirm: the parse left the question unanswered and wrote nothing.
	if e.snapshot(t, vsub) != before || planOf(vsub) != "null" {
		t.Fatal("parse answered the usual-weight question or wrote a row")
	}

	// Confirm, as the test page does: answer the question with set 1's weight, then log the sets.
	e.mustColdStart(t, vc, vw.ID, lateral, fmt.Sprintf(`{"usual_kg":%g}`, float64(*p.UsualWeightG)/1000))
	for _, s := range p.Sets {
		e.mustLogSet(t, vc, vw.ID, p.ExerciseID, s.SetNumber, s.WeightG, s.Reps)
	}
	e.mustComplete(t, vc, vw.ID)

	// Tap user: answers the question with 8 kg and taps the same sets.
	tc, tsub := e.user(t, "tfirst"), e.prefix+"tfirst"
	_, tw := e.start(t, tc)
	e.mustColdStart(t, tc, tw.ID, lateral, `{"usual_kg":8}`)
	for i, reps := range []int{12, 10, 8} {
		e.mustLogSet(t, tc, tw.ID, lateral, i+1, 8000, reps)
	}
	e.mustComplete(t, tc, tw.ID)

	if vp, tp := planOf(vsub), planOf(tsub); vp != tp {
		t.Errorf("stored plans differ:\nvoice %s\ntap   %s", vp, tp)
	}
	if vs, ts := eventSets(vsub), eventSets(tsub); !reflect.DeepEqual(vs, ts) || len(vs) != 3 {
		t.Errorf("event sets differ: voice %+v, tap %+v", vs, ts)
	}

	// Once answered, the exercise has a plan and no usual weight is proposed any more.
	vc2 := e.user(t, "vsecond")
	_, w2 := e.start(t, vc2)
	e.mustColdStart(t, vc2, w2.ID, lateral, `{"usual_kg":8}`)
	if p2 := e.mustVoiceParse(t, vc2, w2.ID, "lateral 8, 12 10 8", ""); p2.UsualWeightG != nil {
		t.Errorf("usual weight proposed for a planned exercise: %+v", p2)
	}
}
