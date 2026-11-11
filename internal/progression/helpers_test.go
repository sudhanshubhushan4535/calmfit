package progression

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// Seed-like configs covering every equipment type, both sizes, both movements and both schemes.
var (
	squat    = Config{ExerciseID: "Barbell_Squat", Equipment: "barbell", Size: Big, Movement: Compound, Scheme: Standard, RepStep: 2}
	calf     = Config{ExerciseID: "Seated_Calf_Raise", Equipment: "machine", Size: Big, Movement: Isolation, Scheme: HighRep, RepStep: 3}
	pulldown = Config{ExerciseID: "Close-Grip_Front_Lat_Pulldown", Equipment: "cable", Size: Big, Movement: Compound, Scheme: Standard, RepStep: 2}
	lateral  = Config{ExerciseID: "Side_Lateral_Raise", Equipment: "dumbbell", Size: Small, Movement: Isolation, Scheme: Standard, RepStep: 2}
	shoulder = Config{ExerciseID: "Dumbbell_Shoulder_Press", Equipment: "dumbbell", Size: Small, Movement: Compound, Scheme: Standard, RepStep: 2}
)

var day0 = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

// g converts kg to grams.
func g(kgs float64) int { return int(math.Round(kgs * 1000)) }

func s(kgs float64, reps int) Set { return Set{WeightG: g(kgs), Reps: reps} }

func usual(kgs float64) ColdStart { return ColdStart{UsualWeightG: g(kgs)} }

var dontKnow = ColdStart{DontKnow: true}

// step logs a session given the plan that was prescribed for it.
type step func(p Plan) []Set

// reps follows the prescribed weights and logs these reps (fewer values = fewer sets logged).
func reps(rs ...int) step {
	return func(p Plan) []Set {
		out := make([]Set, len(rs))
		for i, r := range rs {
			out[i] = Set{WeightG: p.Sets[i].WeightG, Reps: r}
		}
		return out
	}
}

// lifted logs exactly these sets, whatever was prescribed.
func lifted(ss ...Set) step { return func(Plan) []Set { return ss } }

// steady is a calibration session where only set 1 is completed, leaving the exercise in the
// normal phase at the starting weight (R13).
var steady = reps(12, 9, 6)

// simulate runs Next once per step, logging each session, and returns the final Next plan.
// Every plan is checked against the invariants that must always hold.
func simulate(t *testing.T, cfg Config, cold ColdStart, steps ...step) ([]Session, Plan) {
	t.Helper()
	var hist []Session
	for i, st := range steps {
		p := mustNext(t, cfg, hist, cold)
		hist = append(hist, Session{Date: day0.AddDate(0, 0, 2*i), Plan: p, Done: st(p)})
	}
	return hist, mustNext(t, cfg, hist, cold)
}

func mustNext(t *testing.T, cfg Config, hist []Session, cold ColdStart) Plan {
	t.Helper()
	p, err := Next(cfg, hist, cold)
	if err != nil {
		t.Fatalf("Next after %d sessions: %v", len(hist), err)
	}
	checkInvariants(t, cfg, hist, p)
	return p
}

// checkInvariants enforces R1/R3/R14/R16 on every plan the engine produces.
func checkInvariants(t *testing.T, cfg Config, hist []Session, p Plan) {
	t.Helper()
	r, err := cfg.rules()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sets) != SetsPerExercise {
		t.Fatalf("plan has %d sets, want %d", len(p.Sets), SetsPerExercise)
	}
	if p.Summary == "" {
		t.Fatalf("plan has no summary: %+v", p)
	}
	for i, st := range p.Sets {
		if st.Why == "" {
			t.Fatalf("set %d has no why: %+v", i+1, p)
		}
		if i > 0 {
			if d := st.WeightG - p.Sets[i-1].WeightG; d < 0 || d > r.step {
				t.Fatalf("set %d rises %d g over set %d; must be 0..%d (R3/R14): %+v", i+1, d, i, r.step, p.Sets)
			}
		}
	}
	if len(hist) > 0 {
		lastW := hist[len(hist)-1].Done[0].WeightG
		if d := p.Sets[0].WeightG - lastW; d > r.step || d < -r.step {
			t.Fatalf("set 1 moved %d g from last lifted %d g; max one step %d (R14)", d, lastW, r.step)
		}
	}
}

// want describes the expected plan; zero-valued fields are not checked except stalled.
type want struct {
	phase   Phase
	weights []float64 // kg per set
	reps    []int
	why     string // substring of Summary
	stalled bool
}

func assertPlan(t *testing.T, p Plan, w want) {
	t.Helper()
	if w.phase != "" && p.Phase != w.phase {
		t.Errorf("phase = %s, want %s (summary %q)", p.Phase, w.phase, p.Summary)
	}
	if w.weights != nil {
		got := make([]float64, len(p.Sets))
		for i, st := range p.Sets {
			got[i] = float64(st.WeightG) / 1000
		}
		if !slices.Equal(got, w.weights) {
			t.Errorf("weights = %v kg, want %v (summary %q)", got, w.weights, p.Summary)
		}
	}
	if w.reps != nil {
		got := make([]int, len(p.Sets))
		for i, st := range p.Sets {
			got[i] = st.Reps
		}
		if !slices.Equal(got, w.reps) {
			t.Errorf("reps = %v, want %v (summary %q)", got, w.reps, p.Summary)
		}
	}
	if w.why != "" && !strings.Contains(p.Summary, w.why) {
		t.Errorf("summary = %q, want it to contain %q", p.Summary, w.why)
	}
	if p.Stalled != w.stalled {
		t.Errorf("stalled = %v, want %v (summary %q)", p.Stalled, w.stalled, p.Summary)
	}
}

// ruleCase is one row of a rule table: run these sessions, then expect this plan.
type ruleCase struct {
	name  string
	cfg   Config
	cold  ColdStart
	steps []step
	want  want
}

func runRuleTable(t *testing.T, cases []ruleCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, p := simulate(t, tc.cfg, tc.cold, tc.steps...)
			assertPlan(t, p, tc.want)
		})
	}
}

func steps(ss ...step) []step { return ss }

func repeat(n int, st step) []step {
	out := make([]step, n)
	for i := range out {
		out[i] = st
	}
	return out
}
