// Package eval measures voice logging (D36): LLM extraction accuracy on a phrase set, latency and
// cost per call, and, separately, browser speech-to-text accuracy from captured transcripts.
// Scoring is pure Go and tested with a fake parser; only cmd/voice-eval calls the real API.
package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
	"github.com/sudhanshubhushan4535/calmfit/internal/seed"
	"github.com/sudhanshubhushan4535/calmfit/internal/voice"
)

// PlanWeightsG is the synthetic plan every exercise gets in the eval: sets 1–3 at 10 / 12.5 /
// 15 kg. A set with no weight said resolves to these, so expected outputs are stable.
var PlanWeightsG = [3]int{10000, 12500, 15000}

// Case is one line of eval/voice/phrases.jsonl.
type Case struct {
	ID         string `json:"id"`
	Day        string `json:"day"` // push | pull | legs: which exercises are "today's"
	Transcript string `json:"transcript"`
	Hint       string `json:"hint,omitempty"` // exercise open on screen
	// Logged is how many sets each exercise already has (default 0).
	Logged map[string]int `json:"logged,omitempty"`
	// Expect is the resolved result; ExpectReject means any user error (422) is correct.
	Expect       *Expect `json:"expect,omitempty"`
	ExpectReject bool    `json:"expect_reject,omitempty"`
	// Source is "real" (said by a gym member) or "drafted" (written by us). Only real cases count
	// toward the numbers we publish (D36).
	Source string `json:"source"`
}

// Expect is the correct parse after Resolve.
type Expect struct {
	ExerciseID string   `json:"exercise_id"`
	Sets       []ExpSet `json:"sets"`
}

// ExpSet is one expected set.
type ExpSet struct {
	SetNumber int `json:"set_number"`
	WeightG   int `json:"weight_g"`
	Reps      int `json:"reps"`
}

// Catalog is today's candidate list per day, in template order.
type Catalog map[string][]voice.Candidate

// LoadCatalog reads seed/exercises.json and seed/templates.json (template "ppl").
func LoadCatalog(exercisesPath, templatesPath string) (Catalog, error) {
	var exs []seed.Exercise
	if err := readJSON(exercisesPath, &exs); err != nil {
		return nil, err
	}
	var tpls []seed.Template
	if err := readJSON(templatesPath, &tpls); err != nil {
		return nil, err
	}
	byID := map[string]seed.Exercise{}
	for _, e := range exs {
		byID[e.ID] = e
	}
	for _, t := range tpls {
		if t.ID != "ppl" {
			continue
		}
		cat := Catalog{}
		for _, te := range t.Exercises {
			e, ok := byID[te.ExerciseID]
			if !ok {
				return nil, fmt.Errorf("eval: template exercise %s not in %s", te.ExerciseID, exercisesPath)
			}
			cat[te.Day] = append(cat[te.Day], voice.Candidate{ID: e.ID, Name: e.Name, Aliases: e.Aliases})
		}
		return cat, nil
	}
	return nil, fmt.Errorf("eval: template ppl not in %s", templatesPath)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("eval: %w", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("eval: %s: %w", path, err)
	}
	return nil
}

// LoadCases reads a JSONL phrase file and checks every case against the catalog.
func LoadCases(path string, cat Catalog) ([]Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}
	defer f.Close()
	var cases []Case
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "//") {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text))
		dec.DisallowUnknownFields()
		var c Case
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("eval: %s:%d: %w", path, line, err)
		}
		if err := c.validate(cat); err != nil {
			return nil, fmt.Errorf("eval: %s:%d (%s): %w", path, line, c.ID, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("eval: %s:%d: duplicate id %q", path, line, c.ID)
		}
		seen[c.ID] = true
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("eval: %s: %w", path, err)
	}
	return cases, nil
}

func (c Case) validate(cat Catalog) error {
	cands, ok := cat[c.Day]
	switch {
	case c.ID == "":
		return errors.New("id is required")
	case !ok:
		return fmt.Errorf("unknown day %q", c.Day)
	case strings.TrimSpace(c.Transcript) == "":
		return errors.New("transcript is required")
	case c.Source != "real" && c.Source != "drafted":
		return fmt.Errorf("source must be real or drafted, got %q", c.Source)
	case (c.Expect == nil) == !c.ExpectReject:
		return errors.New("exactly one of expect and expect_reject is required")
	}
	inDay := func(id string) bool {
		return slices.ContainsFunc(cands, func(x voice.Candidate) bool { return x.ID == id })
	}
	if c.Hint != "" && !inDay(c.Hint) {
		return fmt.Errorf("hint %s is not a %s exercise", c.Hint, c.Day)
	}
	for id, n := range c.Logged {
		if !inDay(id) || n < 0 || n > 3 {
			return fmt.Errorf("logged %s=%d is invalid for %s", id, n, c.Day)
		}
	}
	if c.Expect != nil {
		if !inDay(c.Expect.ExerciseID) {
			return fmt.Errorf("expected exercise %s is not a %s exercise", c.Expect.ExerciseID, c.Day)
		}
		if len(c.Expect.Sets) == 0 {
			return errors.New("expect needs at least one set")
		}
		for i, s := range c.Expect.Sets {
			if want := c.Logged[c.Expect.ExerciseID] + 1 + i; s.SetNumber != want {
				return fmt.Errorf("expected set %d has set_number %d, want %d", i, s.SetNumber, want)
			}
		}
	}
	return nil
}

// View is the workout Resolve sees for a case: every exercise of the day, planned at PlanWeightsG.
func (c Case) View(cat Catalog) []voice.Exercise {
	var view []voice.Exercise
	for _, cand := range cat[c.Day] {
		n := c.Logged[cand.ID]
		var rem []progression.Target
		for i := n; i < 3; i++ {
			rem = append(rem, progression.Target{WeightG: PlanWeightsG[i]})
		}
		view = append(view, voice.Exercise{ID: cand.ID, Logged: n, Remaining: rem})
	}
	return view
}

// Parser is what the eval runs: a parse plus its token usage.
type Parser interface {
	ParseWithUsage(ctx context.Context, transcript string, candidates []voice.Candidate) (voice.Parsed, voice.Usage, error)
}

// Outcome is one scored case.
type Outcome struct {
	Case     Case
	Got      voice.Result
	Err      error // a user error (422) or a provider error
	Latency  time.Duration
	Usage    voice.Usage
	Exact    bool // the whole result (or the rejection) is right
	Exercise bool // accept cases: right exercise
	Reps     bool // accept cases: every set's reps right
	Weights  bool // accept cases: every set's weight right
	Provider bool // the call failed or broke the schema: counts as wrong
}

// Run parses every case in order (one at a time, so latency isn't skewed by concurrency).
func Run(ctx context.Context, p Parser, cases []Case, cat Catalog, now func() time.Time) []Outcome {
	out := make([]Outcome, 0, len(cases))
	for _, c := range cases {
		start := now()
		parsed, usage, err := p.ParseWithUsage(ctx, c.Transcript, cat[c.Day])
		o := Outcome{Case: c, Latency: now().Sub(start), Usage: usage}
		if err == nil {
			o.Got, err = voice.Resolve(parsed, c.View(cat), c.Hint)
		}
		o.Err = err
		score(&o)
		out = append(out, o)
	}
	return out
}

func score(o *Outcome) {
	_, userErr := voice.IsUserError(o.Err)
	if o.Err != nil && !userErr {
		o.Provider = true
		return
	}
	if o.Case.ExpectReject {
		o.Exact = userErr
		return
	}
	if o.Err != nil {
		return
	}
	exp, got := o.Case.Expect, o.Got
	o.Exercise = got.ExerciseID == exp.ExerciseID
	sameLen := len(got.Sets) == len(exp.Sets)
	o.Reps, o.Weights = sameLen, sameLen
	for i := 0; sameLen && i < len(exp.Sets); i++ {
		g, e := got.Sets[i], exp.Sets[i]
		o.Reps = o.Reps && g.Reps == e.Reps && g.SetNumber == e.SetNumber
		o.Weights = o.Weights && g.WeightG == e.WeightG && g.SetNumber == e.SetNumber
	}
	o.Exact = o.Exercise && o.Reps && o.Weights
}

// Prices are USD per million tokens (input, output) and an optional INR rate (0 = USD only).
type Prices struct {
	InPerMTok, OutPerMTok, INRPerUSD float64
}

// Report summarises outcomes. Rates are fractions of their own denominators.
type Report struct {
	Cases, Accept, Reject                 int
	Exact, Exercise, Reps, Weights        float64 // Exact over all cases; the others over Accept
	RejectRight                           float64 // over Reject
	ProviderErrors                        int
	P50, P95                              time.Duration
	InputTokens, OutputTokens             int64
	CostPerCallUSD, CostPerCallINR, Total float64
	Real, Drafted                         int
}

// Summarise builds a Report.
func Summarise(outs []Outcome, pr Prices) Report {
	r := Report{Cases: len(outs)}
	var exact, ex, reps, wts, rej int
	var lats []time.Duration
	for _, o := range outs {
		if o.Case.Source == "real" {
			r.Real++
		} else {
			r.Drafted++
		}
		if o.Exact {
			exact++
		}
		if o.Case.ExpectReject {
			r.Reject++
			if o.Exact {
				rej++
			}
		} else {
			r.Accept++
			ex += b2i(o.Exercise)
			reps += b2i(o.Reps)
			wts += b2i(o.Weights)
		}
		if o.Provider {
			r.ProviderErrors++
		}
		lats = append(lats, o.Latency)
		r.InputTokens += o.Usage.InputTokens
		r.OutputTokens += o.Usage.OutputTokens
	}
	r.Exact = frac(exact, r.Cases)
	r.Exercise, r.Reps, r.Weights = frac(ex, r.Accept), frac(reps, r.Accept), frac(wts, r.Accept)
	r.RejectRight = frac(rej, r.Reject)
	r.P50, r.P95 = Percentile(lats, 0.50), Percentile(lats, 0.95)
	r.Total = float64(r.InputTokens)*pr.InPerMTok/1e6 + float64(r.OutputTokens)*pr.OutPerMTok/1e6
	if r.Cases > 0 {
		r.CostPerCallUSD = r.Total / float64(r.Cases)
		r.CostPerCallINR = r.CostPerCallUSD * pr.INRPerUSD
	}
	return r
}

// Percentile returns the nearest-rank p-th percentile (0 for no samples).
func Percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func frac(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}
