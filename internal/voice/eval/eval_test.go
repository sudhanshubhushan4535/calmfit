package eval

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/voice"
)

func repoCatalog(t *testing.T) Catalog {
	t.Helper()
	cat, err := LoadCatalog("../../../seed/exercises.json", "../../../seed/templates.json")
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func repoCases(t *testing.T, cat Catalog) []Case {
	t.Helper()
	cases, err := LoadCases("../../../eval/voice/phrases.jsonl", cat)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

// steps returns a clock that advances by d on every call.
func steps(d time.Duration) func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time { t = t.Add(d); return t }
}

func TestRepoPhraseFileIsValid(t *testing.T) {
	cat := repoCatalog(t)
	if len(cat["push"]) != 5 || len(cat["pull"]) != 6 || len(cat["legs"]) != 5 {
		t.Fatalf("catalog sizes push %d pull %d legs %d", len(cat["push"]), len(cat["pull"]), len(cat["legs"]))
	}
	cases := repoCases(t, cat)
	if len(cases) < 40 {
		t.Fatalf("%d cases, want at least 40", len(cases))
	}
	days := map[string]int{}
	for _, c := range cases {
		days[c.Day]++
	}
	if days["push"] == 0 || days["pull"] == 0 || days["legs"] == 0 {
		t.Errorf("cases per day %v: every day needs cases", days)
	}
}

// perfectParser answers like a flawless model: per (day, transcript), the expected exercise with
// every weight said explicitly. Where the case relies on the open exercise (a hint), no exercise
// was spoken, so it names none; reject cases get "not a set log". A transcript can repeat across
// cases ("12 10 8" with and without a hint), which is why the answer depends on what was said only.
type perfectParser struct {
	dayOf   map[string]string // candidate id -> day
	answers map[string]voice.Parsed
}

func perfect(cases []Case, cat Catalog) perfectParser {
	p := perfectParser{dayOf: map[string]string{}, answers: map[string]voice.Parsed{}}
	for day, cands := range cat {
		for _, c := range cands {
			p.dayOf[c.ID] = day
		}
	}
	for _, c := range cases {
		if c.Expect == nil {
			continue
		}
		parsed := voice.Parsed{ExerciseID: c.Expect.ExerciseID, Unit: voice.KG}
		if c.Hint != "" {
			parsed.ExerciseID = ""
		}
		for _, s := range c.Expect.Sets {
			kg := float64(s.WeightG) / 1000
			parsed.Sets = append(parsed.Sets, voice.ParsedSet{Weight: &kg, Reps: s.Reps})
		}
		p.answers[c.Day+"|"+c.Transcript] = parsed
	}
	return p
}

func (p perfectParser) ParseWithUsage(_ context.Context, tr string, cands []voice.Candidate) (voice.Parsed, voice.Usage, error) {
	if parsed, ok := p.answers[p.dayOf[cands[0].ID]+"|"+tr]; ok {
		return parsed, voice.Usage{}, nil
	}
	return voice.Parsed{}, voice.Usage{}, voice.ErrCouldNotParse
}

// Guards the phrase file, Resolve and the scorer together: a perfect parser scores 100%.
func TestPerfectParserScoresFull(t *testing.T) {
	cat := repoCatalog(t)
	cases := repoCases(t, cat)
	outs := Run(context.Background(), perfect(cases, cat), cases, cat, steps(time.Millisecond))
	for _, o := range outs {
		if !o.Exact {
			t.Errorf("%s %q: got %+v err %v, want %+v", o.Case.ID, o.Case.Transcript, o.Got, o.Err, o.Case.Expect)
		}
	}
	r := Summarise(outs, Prices{})
	if r.Exact != 1 || r.Exercise != 1 || r.Reps != 1 || r.Weights != 1 || r.RejectRight != 1 || r.ProviderErrors != 0 {
		t.Errorf("report %+v, want everything at 100%%", r)
	}
	if r.Drafted != len(cases) || r.Real != 0 {
		t.Errorf("real %d drafted %d", r.Real, r.Drafted)
	}
}

func TestScoring(t *testing.T) {
	cat := repoCatalog(t)
	incl := "Incline_Dumbbell_Press"
	cases := []Case{
		{ID: "ok", Day: "push", Transcript: "ok", Source: "real", Expect: &Expect{ExerciseID: incl, Sets: []ExpSet{{1, 20000, 12}, {2, 20000, 10}}}},
		{ID: "reps", Day: "push", Transcript: "reps", Source: "real", Expect: &Expect{ExerciseID: incl, Sets: []ExpSet{{1, 20000, 12}}}},
		{ID: "weight", Day: "push", Transcript: "weight", Source: "real", Expect: &Expect{ExerciseID: incl, Sets: []ExpSet{{1, 20000, 12}}}},
		{ID: "exercise", Day: "push", Transcript: "exercise", Source: "drafted", Expect: &Expect{ExerciseID: incl, Sets: []ExpSet{{1, 20000, 12}}}},
		{ID: "provider", Day: "push", Transcript: "provider", Source: "drafted", Expect: &Expect{ExerciseID: incl, Sets: []ExpSet{{1, 20000, 12}}}},
		{ID: "rej-ok", Day: "push", Transcript: "hello", Source: "drafted", ExpectReject: true},
		{ID: "rej-bad", Day: "push", Transcript: "accepted", Source: "drafted", ExpectReject: true},
	}
	kg := func(v float64) *float64 { return &v }
	fake := &voice.FakeParser{Results: map[string]voice.Parsed{
		"ok":       {ExerciseID: incl, Sets: []voice.ParsedSet{{Weight: kg(20), Reps: 12}, {Reps: 10}}},
		"reps":     {ExerciseID: incl, Sets: []voice.ParsedSet{{Weight: kg(20), Reps: 11}}},
		"weight":   {ExerciseID: incl, Sets: []voice.ParsedSet{{Weight: kg(2), Reps: 12}}},
		"exercise": {ExerciseID: "Butterfly", Sets: []voice.ParsedSet{{Weight: kg(20), Reps: 12}}},
		"accepted": {ExerciseID: incl, Sets: []voice.ParsedSet{{Reps: 8}}},
	}}
	provider := providerFake{fake}
	outs := Run(context.Background(), provider, cases, cat, steps(10*time.Millisecond))

	got := map[string][4]bool{}
	for _, o := range outs {
		got[o.Case.ID] = [4]bool{o.Exact, o.Exercise, o.Reps, o.Weights}
	}
	want := map[string][4]bool{
		"ok":       {true, true, true, true},
		"reps":     {false, true, false, true},
		"weight":   {false, true, true, false},
		"exercise": {false, false, true, true},
		"provider": {false, false, false, false},
		"rej-ok":   {true, false, false, false},
		"rej-bad":  {false, false, false, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scores\n got %v\nwant %v", got, want)
	}

	r := Summarise(outs, Prices{InPerMTok: 0.10, OutPerMTok: 0.50, INRPerUSD: 80})
	if r.Cases != 7 || r.Accept != 5 || r.Reject != 2 || r.Real != 3 || r.Drafted != 4 || r.ProviderErrors != 1 {
		t.Errorf("counts %+v", r)
	}
	approx := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	if !approx(r.Exact, 2.0/7) || !approx(r.Exercise, 3.0/5) || !approx(r.Reps, 3.0/5) || !approx(r.Weights, 3.0/5) || !approx(r.RejectRight, 0.5) {
		t.Errorf("rates exact %v exercise %v reps %v weights %v reject %v", r.Exact, r.Exercise, r.Reps, r.Weights, r.RejectRight)
	}
	// Every call reports 1000 in / 100 out: 7 × (1000×0.10 + 100×0.50) / 1e6 USD.
	if !approx(r.Total, 7*150.0/1e6) || !approx(r.CostPerCallUSD, 150.0/1e6) || !approx(r.CostPerCallINR, 80*150.0/1e6) {
		t.Errorf("cost total %v per call %v (₹%v)", r.Total, r.CostPerCallUSD, r.CostPerCallINR)
	}
	if r.P50 != 10*time.Millisecond || r.P95 != 10*time.Millisecond {
		t.Errorf("latency p50 %v p95 %v", r.P50, r.P95)
	}
}

// providerFake fails the "provider" transcript like a broken API call and reports fixed usage.
type providerFake struct{ *voice.FakeParser }

func (p providerFake) ParseWithUsage(ctx context.Context, tr string, c []voice.Candidate) (voice.Parsed, voice.Usage, error) {
	u := voice.Usage{InputTokens: 1000, OutputTokens: 100}
	if tr == "provider" {
		return voice.Parsed{}, u, errors.Join(voice.ErrProvider, errors.New("503"))
	}
	parsed, err := p.Parse(ctx, tr, c)
	return parsed, u, err
}

func TestPercentile(t *testing.T) {
	ms := func(xs ...int) []time.Duration {
		var ds []time.Duration
		for _, x := range xs {
			ds = append(ds, time.Duration(x)*time.Millisecond)
		}
		return ds
	}
	ds := ms(900, 100, 300, 200, 500, 400, 700, 600, 800, 1000) // 10 samples, unsorted
	if got := Percentile(ds, 0.50); got != 500*time.Millisecond {
		t.Errorf("p50 = %v", got)
	}
	if got := Percentile(ds, 0.95); got != 1000*time.Millisecond {
		t.Errorf("p95 = %v", got)
	}
	if got := Percentile(nil, 0.5); got != 0 {
		t.Errorf("empty = %v", got)
	}
	if ds[0] != 900*time.Millisecond {
		t.Error("Percentile sorted its input in place")
	}
}

func TestLoadCasesRejectsBadLines(t *testing.T) {
	cat := repoCatalog(t)
	for name, line := range map[string]string{
		"unknown day":    `{"id":"x","day":"arms","transcript":"t","expect_reject":true,"source":"drafted"}`,
		"both expects":   `{"id":"x","day":"push","transcript":"t","expect_reject":true,"expect":{"exercise_id":"Butterfly","sets":[{"set_number":1,"weight_g":1,"reps":1}]},"source":"drafted"}`,
		"neither":        `{"id":"x","day":"push","transcript":"t","source":"drafted"}`,
		"wrong day ex":   `{"id":"x","day":"push","transcript":"t","expect":{"exercise_id":"Barbell_Squat","sets":[{"set_number":1,"weight_g":1,"reps":1}]},"source":"drafted"}`,
		"set numbering":  `{"id":"x","day":"push","transcript":"t","expect":{"exercise_id":"Butterfly","sets":[{"set_number":2,"weight_g":1,"reps":1}]},"source":"drafted"}`,
		"bad source":     `{"id":"x","day":"push","transcript":"t","expect_reject":true,"source":"made-up"}`,
		"unknown field":  `{"id":"x","day":"push","transcript":"t","expect_reject":true,"source":"drafted","note":1}`,
		"hint wrong day": `{"id":"x","day":"push","transcript":"t","hint":"Barbell_Squat","expect_reject":true,"source":"drafted"}`,
	} {
		path := t.TempDir() + "/p.jsonl"
		if err := writeFile(path, line+"\n"); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCases(path, cat); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestSTT(t *testing.T) {
	for in, want := range map[string][]string{
		"Incline 12.5, twelve. 3.Ten": {"incline", "12.5", "12", "3", "10"},
		"forty five kilos, twenty":    {"45", "kg", "20"},
		"Twenty kg twelve ten eight":  {"20", "kg", "12", "10", "8"},
		"135 pounds; ten lbs":         {"135", "lb", "10", "lb"},
		"pulldown bara das aath":      {"pulldown", "bara", "das", "aath"}, // Hindi numbers stay words
	} {
		if got := Words(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Words(%q) = %q, want %q", in, got, want)
		}
	}
	r := ScoreSTT([]STTRecord{
		{ID: "a", SpokenText: "incline 20, 12 10 8", Transcript: "Incline 20 12 10 8"},                     // exact after normalising
		{ID: "b", SpokenText: "incline 20 12 10 8", Transcript: "in climb 20 12 10 8"},                     // 1 sub + 1 insert
		{ID: "c", SpokenText: "squat 60", Transcript: ""},                                                  // 2 deletions
		{ID: "d", SpokenText: "incline twenty kg twelve ten eight", Transcript: "incline 20 kilo 12 10 8"}, // exact: digits for words
	})
	if r.Records != 4 || math.Abs(r.ExactMatch-2.0/4) > 1e-9 {
		t.Errorf("exact = %v over %d", r.ExactMatch, r.Records)
	}
	// edits 0 + 2 + 2 + 0 = 4 over 5 + 5 + 2 + 6 = 18 reference words.
	if math.Abs(r.WER-4.0/18) > 1e-9 {
		t.Errorf("WER = %v, want %v", r.WER, 4.0/18)
	}
}

func writeFile(path, s string) error { return os.WriteFile(path, []byte(s), 0o600) }
