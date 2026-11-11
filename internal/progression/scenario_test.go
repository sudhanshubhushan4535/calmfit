package progression

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// turn: expect this plan from Next, then log this session (nil = stop).
type turn struct {
	want want
	log  step
}

// play runs a whole history turn by turn, checking every plan along the way.
func play(t *testing.T, cfg Config, cold ColdStart, turns []turn) []Session {
	t.Helper()
	var hist []Session
	for i, tr := range turns {
		p := mustNext(t, cfg, hist, cold)
		t.Logf("session %d: %s %v — %s", i+1, p.Phase, kgList(p), p.Summary)
		assertPlan(t, p, tr.want)
		if t.Failed() {
			t.FailNow()
		}
		if tr.log == nil {
			break
		}
		hist = append(hist, Session{Date: day0.AddDate(0, 0, 2*i), Plan: p, Done: tr.log(p)})
	}
	return hist
}

func kgList(p Plan) []float64 {
	out := make([]float64, len(p.Sets))
	for i, st := range p.Sets {
		out[i] = float64(st.WeightG) / 1000
	}
	return out
}

// A17b, end to end: after a small-muscle increase all sets go up; set 1 is under 12 and set 2
// misses, so set 3 is held in-session and capped from the next session until set 1 hits 12.
func TestScenario_SmallMuscleIncreaseThenCap_A17b(t *testing.T) {
	cfg := lateral
	var hist []Session
	next := func() Plan { return mustNext(t, cfg, hist, usual(5)) }
	log := func(p Plan, done ...Set) {
		hist = append(hist, Session{Date: day0.AddDate(0, 0, 2*len(hist)), Plan: p, Done: done})
	}

	// 1. Calibration at 5 kg; only set 1 completed -> stay at 5.
	p := next()
	assertPlan(t, p, want{phase: PhaseCalibration, weights: []float64{5, 7.5, 10}})
	log(p, s(5, 12), s(7.5, 9), s(10, 6))

	// 2. Normal at 5; set 1 hits 13 -> increase.
	p = next()
	assertPlan(t, p, want{phase: PhaseNormal, weights: []float64{5, 7.5, 10}})
	log(p, s(5, 13), s(7.5, 10), s(10, 8))

	// 3. After the increase ALL sets go up one step (A17b).
	p = next()
	assertPlan(t, p, want{phase: PhaseBuilding, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}, why: "+2.5 kg"})

	//    In-session: set 1 gets 11 of 12 -> set 2 holds at 7.5 aiming for 12; set 3 one step above (R10, R14).
	rest, err := AdjustInSession(cfg, p, []Set{s(7.5, 11)})
	if err != nil || len(rest) != 2 || rest[0] != (Target{g(7.5), 12, "Hold: set 1 missed its reps"}) || rest[1].WeightG != g(10) {
		t.Fatalf("after set 1 missed: %+v, %v", rest, err)
	}
	//    Set 2 gets 9 (below its prescribed 10) -> set 3 stays at set 2's weight.
	rest, err = AdjustInSession(cfg, p, []Set{s(7.5, 11), s(7.5, 9)})
	if err != nil || len(rest) != 1 || rest[0].WeightG != g(7.5) || rest[0].Why != "Hold: set 2 missed its reps" {
		t.Fatalf("after set 2 missed: %+v, %v", rest, err)
	}
	log(p, s(7.5, 11), s(7.5, 9), s(7.5, 8))

	// 4. From the next session the cap persists: set 3 = set 2's weight and reps.
	p = next()
	assertPlan(t, p, want{phase: PhaseCapped, weights: []float64{7.5, 10, 10}, reps: []int{12, 10, 10}, why: "set 3 stays at set 2's weight"})
	log(p, s(7.5, 11), s(10, 10), s(10, 9))

	// 5. Set 1 still under 12 -> still capped.
	p = next()
	assertPlan(t, p, want{phase: PhaseCapped, weights: []float64{7.5, 10, 10}, reps: []int{12, 10, 10}, why: "until set 1 hits 12"})
	log(p, s(7.5, 12), s(10, 10), s(10, 10))

	// 6. Set 1 reached 12 -> set 3 gets its proper heavier weight again.
	p = next()
	assertPlan(t, p, want{phase: PhaseNormal, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}, why: "Set 3 back to its full weight"})
	log(p, s(7.5, 13), s(10, 10), s(12.5, 8))

	// 7. And the next increase works normally.
	p = next()
	assertPlan(t, p, want{phase: PhaseBuilding, weights: []float64{10, 12.5, 15}, why: "+2.5 kg: you hit 13"})
}

// A17b counter-case: set 1 reaches the top at the new weight, set 2 misses. The miss is handled
// in-session only (set 3 held at set 2's weight); the next session is uncapped.
func TestScenario_SmallMuscleSet1AtTopNoCap_A17b(t *testing.T) {
	cfg := lateral
	hist, p := simulate(t, cfg, usual(5), steady, reps(13, 10, 8))
	assertPlan(t, p, want{phase: PhaseBuilding, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}})

	rest, err := AdjustInSession(cfg, p, []Set{s(7.5, 12), s(10, 8)})
	if err != nil || len(rest) != 1 || rest[0] != (Target{g(10), 10, "Hold: set 2 missed its reps"}) {
		t.Fatalf("in-session after set 2 missed: %+v, %v", rest, err)
	}
	hist = append(hist, Session{Date: day0.AddDate(0, 0, 10), Plan: p, Done: []Set{s(7.5, 12), s(10, 8), s(10, 9)}})

	next := mustNext(t, cfg, hist, usual(5))
	assertPlan(t, next, want{phase: PhaseNormal, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}, why: "Hold: 12 reps"})
}

// A big-muscle exercise through cold start, increase, shift, build back, failures and a drop.
func TestScenario_BigMuscleFullCycle(t *testing.T) {
	play(t, squat, usual(60), []turn{
		{want{phase: PhaseCalibration, weights: []float64{60, 62.5, 65}, reps: []int{12, 10, 8}}, reps(12, 9, 6)},
		{want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "heaviest completed set"}, reps(13, 10, 8)},
		{want{phase: PhaseBuilding, weights: []float64{62.5, 65, 67.5}, why: "+2.5 kg: you hit 13"}, reps(10, 8, 6)},
		{want{phase: PhaseShifted, weights: []float64{62.5, 65, 67.5}, reps: []int{10, 8, 6}}, reps(12, 9, 7)},
		{want{phase: PhaseNormal, weights: []float64{62.5, 65, 67.5}, reps: []int{12, 10, 8}, why: "back to 12/10/8"}, reps(13, 10, 8)},
		{want{phase: PhaseBuilding, weights: []float64{65, 67.5, 70}}, reps(9, 7, 5)},
		{want{phase: PhaseBuilding, weights: []float64{65, 67.5, 70}, why: "below 10"}, reps(9, 7, 5)},
		{want{phase: PhaseNormal, weights: []float64{62.5, 65, 67.5}, why: "-2.5 kg: set 1 was under 10 reps two sessions in a row"}, nil},
	})
}

// High-rep big muscle (machine): 19 to go up, 5 kg steps, 15/12/9 shift.
func TestScenario_HighRepMachine(t *testing.T) {
	play(t, calf, dontKnow, []turn{
		{want{phase: PhaseCalibration, weights: []float64{5, 10, 15}, reps: []int{18, 15, 12}}, reps(18, 15, 12)},
		{want{phase: PhaseBuilding, weights: []float64{10, 15, 20}, why: "first session, you completed set 3"}, reps(15, 12, 10)},
		{want{phase: PhaseShifted, weights: []float64{10, 15, 20}, reps: []int{15, 12, 9}}, reps(18, 13, 10)},
		{want{phase: PhaseNormal, weights: []float64{10, 15, 20}, reps: []int{18, 15, 12}}, reps(19, 15, 12)},
		{want{phase: PhaseBuilding, weights: []float64{15, 20, 25}, why: "+5 kg: you hit 19"}, nil},
	})
}

// Sanity: every rule test file references a rule documented in docs/progression.md.
func TestRuleIDsAreDocumented(t *testing.T) {
	ids := []string{"R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10", "R11", "R12", "R13", "R14", "R15", "R16", "R17"}
	doc := readDoc(t)
	for _, id := range ids {
		if !slices.ContainsFunc(doc, func(line string) bool { return strings.HasPrefix(line, "## "+id+". ") }) {
			t.Errorf("rule %s has a test but no '## %s.' heading in docs/progression.md", id, id)
		}
	}
}

func readDoc(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("../../docs/progression.md")
	if err != nil {
		t.Fatalf("read docs/progression.md: %v", err)
	}
	return strings.Split(string(b), "\n")
}
