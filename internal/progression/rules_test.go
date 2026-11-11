package progression

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"
)

// R1: rep targets per scheme, normal and shifted.
func TestR1_Targets(t *testing.T) {
	tests := []struct {
		scheme  RepScheme
		step    int
		shifted bool
		want    []int
		wantErr error
	}{
		{Standard, 2, false, []int{12, 10, 8}, nil},
		{Standard, 2, true, []int{10, 8, 6}, nil},
		{HighRep, 3, false, []int{18, 15, 12}, nil},
		{HighRep, 3, true, []int{15, 12, 9}, nil},
		{Standard, 3, false, nil, ErrConfig}, // rep_step must match the scheme
		{HighRep, 2, false, nil, ErrConfig},
		{"pyramid", 2, false, nil, ErrConfig},
	}
	for _, tt := range tests {
		got, err := Targets(tt.scheme, tt.step, tt.shifted)
		if !errors.Is(err, tt.wantErr) || !slices.Equal(got, tt.want) {
			t.Errorf("Targets(%s, %d, %v) = %v, %v; want %v, %v", tt.scheme, tt.step, tt.shifted, got, err, tt.want, tt.wantErr)
		}
	}
}

// R2: weight step per equipment.
func TestR2_StepFor(t *testing.T) {
	tests := []struct {
		equipment string
		want      int
		wantErr   bool
	}{
		{"barbell", 2500, false},
		{"dumbbell", 2500, false},
		{"machine", 5000, false},
		{"cable", 5000, false},
		{"kettlebells", 0, true},
		{"body only", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := StepFor(tt.equipment)
		if got != tt.want || (err != nil) != tt.wantErr || (err != nil && !errors.Is(err, ErrConfig)) {
			t.Errorf("StepFor(%q) = %d, %v; want %d, err=%v", tt.equipment, got, err, tt.want, tt.wantErr)
		}
	}
}

// R3: weights ascend one step per set; covered on every plan by checkInvariants, plus explicit rows.
func TestR3_AscendingWeights(t *testing.T) {
	runRuleTable(t, []ruleCase{
		{name: "barbell ascends 2.5 kg per set", cfg: squat, cold: usual(60),
			want: want{weights: []float64{60, 62.5, 65}}},
		{name: "machine ascends 5 kg per set", cfg: calf, cold: usual(40),
			want: want{weights: []float64{40, 45, 50}}},
	})
}

// R4: warm-ups, compound only.
func TestR4_Warmups(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		set1 float64
		want []float64 // warm-up weights, kg; each is 2 reps
	}{
		{"barbell 60 → 30, 45", squat, 60, []float64{30, 45}},
		{"barbell 20: nothing below the empty bar", squat, 20, nil},
		{"barbell 22.5: both round to the bar, merged", squat, 22.5, []float64{20}},
		{"cable 25: 12.5 ties down to 10, 18.75 rounds to 20", pulldown, 25, []float64{10, 20}},
		{"dumbbell 10 → 5, 7.5", shoulder, 10, []float64{5, 7.5}},
		{"dumbbell 5: both round to 2.5, merged", shoulder, 5, []float64{2.5}},
		{"dumbbell 2.5: none below set 1", shoulder, 2.5, nil},
		{"isolation exercise: no warm-ups", lateral, 20, nil},
		{"isolation big muscle: no warm-ups", calf, 60, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := tt.cfg.rules()
			if err != nil {
				t.Fatal(err)
			}
			ws := r.warmups(g(tt.set1))
			var got []float64
			for _, w := range ws {
				if w.Reps != WarmupReps {
					t.Fatalf("warm-up reps = %d, want %d", w.Reps, WarmupReps)
				}
				got = append(got, float64(w.WeightG)/1000)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("warm-ups = %v kg, want %v", got, tt.want)
			}
		})
	}

	// And they come through Next: compound gets them, isolation gets an empty (non-nil) list.
	if p := mustNext(t, squat, nil, usual(60)); len(p.Warmups) != 2 {
		t.Errorf("squat plan warm-ups = %v, want 2", p.Warmups)
	}
	if p := mustNext(t, lateral, nil, usual(10)); p.Warmups == nil || len(p.Warmups) != 0 {
		t.Errorf("lateral raise plan warm-ups = %#v, want empty", p.Warmups)
	}
}

// R5: set 1 above the top moves all sets up one step.
func TestR5_Increase(t *testing.T) {
	runRuleTable(t, []ruleCase{
		{name: "13 on set 1 moves all sets up one step", cfg: squat, cold: usual(60),
			steps: steps(steady, reps(13, 10, 8)),
			want:  want{phase: PhaseBuilding, weights: []float64{62.5, 65, 67.5}, reps: []int{12, 10, 8}, why: "+2.5 kg: you hit 13 reps last time"}},
		{name: "only set 1 decides, even if sets 2-3 missed (A10)", cfg: squat, cold: usual(60),
			steps: steps(steady, reps(13, 7, 5)),
			want:  want{phase: PhaseBuilding, weights: []float64{62.5, 65, 67.5}}},
		{name: "high_rep needs 19; machine steps 5 kg", cfg: calf, cold: usual(40),
			steps: steps(reps(18, 14, 10), reps(19, 15, 12)),
			want:  want{phase: PhaseBuilding, weights: []float64{45, 50, 55}, reps: []int{18, 15, 12}, why: "+5 kg: you hit 19 reps"}},
		{name: "18 on high_rep holds", cfg: calf, cold: usual(40),
			steps: steps(reps(18, 14, 10), reps(18, 15, 12)),
			want:  want{phase: PhaseNormal, weights: []float64{40, 45, 50}, why: "hit 19 to go up"}},
		{name: "small muscles increase all sets too (A17b)", cfg: lateral, cold: usual(5),
			steps: steps(steady, reps(13, 10, 8)),
			want:  want{phase: PhaseBuilding, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}}},
		{name: "logged weight is the truth (R14): user lifted 70, not 60", cfg: squat, cold: usual(60),
			steps: steps(steady, lifted(s(70, 13), s(72.5, 10), s(75, 8))),
			want:  want{phase: PhaseBuilding, weights: []float64{72.5, 75, 77.5}}},
	})
}

// R6: hold zone repeats the weight at any time.
func TestR6_HoldZone(t *testing.T) {
	runRuleTable(t, []ruleCase{
		{name: "12 holds", cfg: squat, cold: usual(60), steps: steps(steady, reps(12, 10, 8)),
			want: want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "Hold: 12 reps on set 1; hit 13 to go up"}},
		{name: "11 holds", cfg: squat, cold: usual(60), steps: steps(steady, reps(11, 10, 8)),
			want: want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "Hold: 11 reps"}},
		{name: "10 (top - rep_step) holds", cfg: squat, cold: usual(60), steps: steps(steady, reps(10, 10, 8)),
			want: want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "Hold: 10 reps"}},
		{name: "high_rep 15 holds", cfg: calf, cold: usual(40), steps: steps(reps(18, 14, 10), reps(15, 12, 10)),
			want: want{phase: PhaseNormal, weights: []float64{40, 45, 50}, why: "Hold: 15 reps"}},
	})
}

// R7: one failure holds; two in a row drop one step; never below the lightest option.
func TestR7_Failure(t *testing.T) {
	runRuleTable(t, []ruleCase{
		{name: "one failure holds", cfg: squat, cold: usual(60), steps: steps(steady, reps(9, 8, 6)),
			want: want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "Hold: set 1 got 9, below 10"}},
		{name: "two failures in a row drop one step", cfg: squat, cold: usual(60), steps: steps(steady, reps(9, 8, 6), reps(9, 8, 6)),
			want: want{phase: PhaseNormal, weights: []float64{57.5, 60, 62.5}, reps: []int{12, 10, 8}, why: "-2.5 kg: set 1 was under 10 reps two sessions in a row"}},
		{name: "a non-failure in between resets the streak", cfg: squat, cold: usual(60), steps: steps(steady, reps(9), reps(11), reps(9)),
			want: want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "below 10"}},
		{name: "high_rep failure is 14 or less; machine drops 5 kg", cfg: calf, cold: usual(40), steps: steps(reps(18, 14, 10), reps(14, 12, 10), reps(14, 12, 10)),
			want: want{phase: PhaseNormal, weights: []float64{35, 40, 45}, why: "-5 kg"}},
		{name: "calibration failure counts toward the streak", cfg: squat, cold: usual(60), steps: steps(reps(8, 6, 5), reps(8, 6, 5)),
			want: want{phase: PhaseNormal, weights: []float64{57.5, 60, 62.5}, why: "-2.5 kg"}},
		{name: "drop never goes below the lightest option", cfg: lateral, cold: dontKnow, steps: steps(reps(5), reps(5)),
			want: want{phase: PhaseNormal, weights: []float64{2.5, 5, 7.5}, why: "Hold at 2.5 kg, the lightest option"}},
		{name: "empty bar is the barbell floor", cfg: squat, cold: dontKnow, steps: steps(reps(7), reps(7)),
			want: want{phase: PhaseNormal, weights: []float64{20, 22.5, 25}, why: "lightest option"}},
	})
}

// R8: big muscles after an increase: shift targets on exactly top-rep_step, build back first.
func TestR8_BigAfterIncrease(t *testing.T) {
	inc := steps(steady, reps(13, 10, 8)) // squat now building at 62.5
	with := func(more ...step) []step { return append(slices.Clone(inc), more...) }
	runRuleTable(t, []ruleCase{
		{name: "10 after an increase shifts targets to 10/8/6, weights still ascend", cfg: squat, cold: usual(60),
			steps: with(reps(10, 8, 6)),
			want:  want{phase: PhaseShifted, weights: []float64{62.5, 65, 67.5}, reps: []int{10, 8, 6}, why: "Same weight, targets 10/8/6: set 1 reached only 10 after the increase"}},
		{name: "11 after an increase holds normal targets", cfg: squat, cold: usual(60),
			steps: with(reps(11, 9, 7)),
			want:  want{phase: PhaseBuilding, weights: []float64{62.5, 65, 67.5}, reps: []int{12, 10, 8}, why: "Hold: 11 reps"}},
		{name: "12 after an increase ends the post-increase phase", cfg: squat, cold: usual(60),
			steps: with(reps(12, 10, 8)),
			want:  want{phase: PhaseNormal, weights: []float64{62.5, 65, 67.5}, reps: []int{12, 10, 8}}},
		{name: "9 after an increase is a failure, not a shift", cfg: squat, cold: usual(60),
			steps: with(reps(9, 8, 6)),
			want:  want{phase: PhaseBuilding, weights: []float64{62.5, 65, 67.5}, reps: []int{12, 10, 8}, why: "below 10"}},
		{name: "shifted holds until set 1 is back to 12", cfg: squat, cold: usual(60),
			steps: with(reps(10, 8, 6), reps(11, 8, 6)),
			want:  want{phase: PhaseShifted, weights: []float64{62.5, 65, 67.5}, reps: []int{10, 8, 6}, why: "build set 1 back to 12"}},
		{name: "set 1 back to 12: normal targets at the same weight", cfg: squat, cold: usual(60),
			steps: with(reps(10, 8, 6), reps(12, 8, 6)),
			want:  want{phase: PhaseNormal, weights: []float64{62.5, 65, 67.5}, reps: []int{12, 10, 8}, why: "back to 12/10/8"}},
		{name: "13 while shifted builds back first, no increase (A16)", cfg: squat, cold: usual(60),
			steps: with(reps(10, 8, 6), reps(13, 9, 7)),
			want:  want{phase: PhaseNormal, weights: []float64{62.5, 65, 67.5}, reps: []int{12, 10, 8}, why: "back to 12/10/8"}},
		{name: "failure while shifted keeps the shift", cfg: squat, cold: usual(60),
			steps: with(reps(10, 8, 6), reps(8, 7, 6)),
			want:  want{phase: PhaseShifted, weights: []float64{62.5, 65, 67.5}, reps: []int{10, 8, 6}, why: "below 10"}},
		{name: "high_rep shift is 15/12/9", cfg: calf, cold: usual(40),
			steps: steps(reps(18, 14, 10), reps(19, 15, 12), reps(15, 12, 9)),
			want:  want{phase: PhaseShifted, weights: []float64{45, 50, 55}, reps: []int{15, 12, 9}}},
		{name: "small muscles never shift (A15)", cfg: lateral, cold: usual(5),
			steps: steps(steady, reps(13, 10, 8), reps(10, 10, 8)),
			want:  want{phase: PhaseBuilding, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}, why: "Hold: 10 reps"}},
	})
}

// R9: small muscles after an increase: a set-2 miss caps set 3 at set 2's weight until set 1 hits the top (A17b).
func TestR9_SmallAfterIncrease(t *testing.T) {
	inc := steps(steady, reps(13, 10, 8))                // lateral raise now building at 7.5 / 10 / 12.5
	missSet2 := lifted(s(7.5, 11), s(7.5, 9), s(7.5, 8)) // set 1 under the top, set 2 missed (both held in-session, R10)
	with := func(more ...step) []step { return append(slices.Clone(inc), more...) }
	capped := want{phase: PhaseCapped, weights: []float64{7.5, 10, 10}, reps: []int{12, 10, 10}}
	runRuleTable(t, []ruleCase{
		{name: "set 1 under the top and set 2 missed after the increase: set 3 capped at set 2's weight and reps", cfg: lateral, cold: usual(5),
			steps: with(missSet2),
			want:  want{phase: capped.phase, weights: capped.weights, reps: capped.reps, why: "set 3 stays at set 2's weight"}},
		{name: "set 1 at 10 with set 2 missed also caps", cfg: lateral, cold: usual(5),
			steps: with(lifted(s(7.5, 10), s(7.5, 9), s(7.5, 8))),
			want:  capped},
		{name: "set 1 at the top with set 2 missed: in-session only, next session uncapped", cfg: lateral, cold: usual(5),
			steps: with(lifted(s(7.5, 12), s(10, 8), s(10, 9))),
			want:  want{phase: PhaseNormal, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}}},
		{name: "set 2 held in-session and hitting its target is not a miss", cfg: lateral, cold: usual(5),
			steps: with(lifted(s(7.5, 11), s(7.5, 12), s(10, 8))),
			want:  want{phase: PhaseBuilding, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}}},
		{name: "set 2 not logged counts as missed (A9)", cfg: lateral, cold: usual(5),
			steps: with(reps(11)),
			want:  capped},
		{name: "cap persists while set 1 is under 12", cfg: lateral, cold: usual(5),
			steps: with(missSet2, reps(11, 10, 10)),
			want:  want{phase: PhaseCapped, weights: capped.weights, reps: capped.reps, why: "until set 1 hits 12"}},
		{name: "set 1 hits 12: set 3 gets its full weight again", cfg: lateral, cold: usual(5),
			steps: with(missSet2, reps(12, 10, 10)),
			want:  want{phase: PhaseNormal, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}, why: "Set 3 back to its full weight"}},
		{name: "13 while capped uncaps first, no increase", cfg: lateral, cold: usual(5),
			steps: with(missSet2, reps(13, 10, 10)),
			want:  want{phase: PhaseNormal, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}}},
		{name: "failure while capped keeps the cap", cfg: lateral, cold: usual(5),
			steps: with(missSet2, reps(9, 8, 8)),
			want:  want{phase: PhaseCapped, weights: capped.weights, reps: capped.reps, why: "below 10"}},
		{name: "set 2 hit after the increase: no cap", cfg: lateral, cold: usual(5),
			steps: with(reps(11, 10, 8)),
			want:  want{phase: PhaseBuilding, weights: []float64{7.5, 10, 12.5}, reps: []int{12, 10, 8}}},
		{name: "set 1 failed after the increase: failure rule, no cap", cfg: lateral, cold: usual(5),
			steps: with(reps(9, 8, 8)),
			want:  want{phase: PhaseBuilding, weights: []float64{7.5, 10, 12.5}, why: "below 10"}},
		{name: "big muscles never cap", cfg: squat, cold: usual(60),
			steps: steps(steady, reps(13, 10, 8), reps(11, 8, 7)),
			want:  want{phase: PhaseBuilding, weights: []float64{62.5, 65, 67.5}, reps: []int{12, 10, 8}}},
	})
}

// R10: in-session adjustment, small muscles only.
func TestR10_InSession(t *testing.T) {
	_, latPlan := simulate(t, lateral, usual(5), steady) // 5 / 7.5 / 10 at 12/10/8
	_, squatPlan := simulate(t, squat, usual(60), steady)

	tests := []struct {
		name    string
		cfg     Config
		plan    Plan
		done    []Set
		want    []Set // remaining targets (weight kg, reps)
		heldWhy bool  // first remaining target explains the hold
	}{
		{"no miss: plan unchanged", lateral, latPlan, []Set{s(5, 12)}, []Set{s(7.5, 10), s(10, 8)}, false},
		{"set 1 missed: set 2 repeats its weight and target; set 3 one step above (R14)", lateral, latPlan, []Set{s(5, 11)}, []Set{s(5, 12), s(7.5, 8)}, true},
		{"misses cascade", lateral, latPlan, []Set{s(5, 11), s(5, 10)}, []Set{s(5, 12)}, true},
		{"set 2 missed: set 3 stays at set 2's weight and target", lateral, latPlan, []Set{s(5, 12), s(7.5, 9)}, []Set{s(7.5, 10)}, true},
		{"logged weight is the truth: set 3 one step above what was lifted", lateral, latPlan, []Set{s(5, 12), s(5, 10)}, []Set{s(7.5, 8)}, false},
		{"nothing done yet: full plan", lateral, latPlan, nil, []Set{s(5, 12), s(7.5, 10), s(10, 8)}, false},
		{"all done: nothing remains", lateral, latPlan, []Set{s(5, 12), s(7.5, 10), s(10, 8)}, []Set{}, false},
		{"big muscles: no in-session changes", squat, squatPlan, []Set{s(60, 9)}, []Set{s(62.5, 10), s(65, 8)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AdjustInSession(tt.cfg, tt.plan, tt.done)
			if err != nil {
				t.Fatal(err)
			}
			gotSets := make([]Set, len(got))
			for i, tg := range got {
				gotSets[i] = Set{WeightG: tg.WeightG, Reps: tg.Reps}
				if tg.Why == "" {
					t.Fatalf("target %d has no why", i)
				}
			}
			if !slices.Equal(gotSets, tt.want) {
				t.Fatalf("remaining = %v, want %v", gotSets, tt.want)
			}
			if tt.heldWhy && got[0].Why != "Hold: set "+strconv.Itoa(len(tt.done))+" missed its reps" {
				t.Fatalf("why = %q, want the hold reason", got[0].Why)
			}
		})
	}

	errs := []struct {
		name string
		done []Set
	}{
		{"more sets done than planned", []Set{s(5, 12), s(7.5, 10), s(10, 8), s(10, 8)}},
		{"0 reps", []Set{s(5, 0)}},
		{"51 reps", []Set{s(5, 51)}},
		{"negative weight", []Set{{WeightG: -2500, Reps: 10}}},
	}
	for _, tt := range errs {
		if _, err := AdjustInSession(lateral, latPlan, tt.done); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", tt.name, err)
		}
	}
}

// R11: progress definition.
func TestR11_Progress(t *testing.T) {
	sess := func(sets ...Set) Session { return Session{Done: sets} }
	tests := []struct {
		name      string
		cur, prev Session
		want      bool
	}{
		{"heavier set 1", sess(s(62.5, 10)), sess(s(60, 12), s(62.5, 10), s(65, 8)), true},
		{"same weight, more total reps", sess(s(60, 12), s(62.5, 10), s(65, 7)), sess(s(60, 12), s(62.5, 9), s(65, 6)), true},
		{"same weight, same total", sess(s(60, 11), s(62.5, 10), s(65, 8)), sess(s(60, 12), s(62.5, 9), s(65, 8)), false},
		{"same weight, fewer reps", sess(s(60, 11), s(62.5, 9), s(65, 6)), sess(s(60, 12), s(62.5, 9), s(65, 6)), false},
		{"lighter set 1 is never progress, even with more reps", sess(s(57.5, 13), s(60, 11), s(62.5, 9)), sess(s(60, 9), s(62.5, 8), s(65, 6)), false},
		{"fewer sets logged, fewer total reps", sess(s(60, 12)), sess(s(60, 12), s(62.5, 9), s(65, 6)), false},
	}
	for _, tt := range tests {
		if got := progressed(tt.cur, tt.prev); got != tt.want {
			t.Errorf("%s: progressed = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// R12: stall after 3 sessions without progress, with a fix chosen by shortfall.
func TestR12_Stall(t *testing.T) {
	runRuleTable(t, []ruleCase{
		{name: "two sessions without progress is not a stall", cfg: squat, cold: usual(60),
			steps: append(steps(steady), repeat(2, reps(11, 9, 7))...),
			want:  want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, stalled: false}},
		{name: "0 short (targets met, never 13): flag only, same weight", cfg: squat, cold: usual(60),
			steps: append(steps(steady), repeat(4, reps(12, 10, 8))...),
			want:  want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, reps: []int{12, 10, 8}, why: "Stalled at 12 reps: push set 1 for 13", stalled: true}},
		{name: "1-2 reps short: same weight, targets lowered by rep_step", cfg: squat, cold: usual(60),
			steps: append(steps(steady), repeat(4, reps(12, 10, 7))...),
			want:  want{phase: PhaseShifted, weights: []float64{60, 62.5, 65}, reps: []int{10, 8, 6}, why: "Stalled 3 sessions, 1 rep short", stalled: true}},
		{name: "3+ reps short: drop one step", cfg: squat, cold: usual(60),
			steps: append(steps(steady), repeat(3, reps(11, 9, 7))...),
			want:  want{phase: PhaseNormal, weights: []float64{57.5, 60, 62.5}, reps: []int{12, 10, 8}, why: "-2.5 kg: stalled 3 sessions, 3 reps short", stalled: true}},
		{name: "an unlogged set counts as missed in the shortfall", cfg: squat, cold: usual(60),
			steps: append(steps(steady), repeat(3, reps(12, 10))...),
			want:  want{phase: PhaseNormal, weights: []float64{57.5, 60, 62.5}, why: "8 reps short", stalled: true}},
		{name: "the counter resets after a fix", cfg: squat, cold: usual(60),
			steps: append(append(steps(steady), repeat(3, reps(11, 9, 7))...), reps(11, 9, 7)),
			want:  want{phase: PhaseNormal, weights: []float64{57.5, 60, 62.5}, stalled: false}},
		{name: "one progressing session resets the count", cfg: squat, cold: usual(60),
			steps: steps(steady, reps(11, 9, 7), reps(11, 9, 7), reps(11, 10, 7), reps(11, 10, 7), reps(11, 10, 7)),
			want:  want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, stalled: false}},
		{name: "two failures drop before a stall can (R7 precedence in practice)", cfg: squat, cold: usual(60),
			steps: steps(steady, reps(9, 8, 6), reps(9, 8, 6)),
			want:  want{phase: PhaseNormal, weights: []float64{57.5, 60, 62.5}, why: "two sessions in a row", stalled: false}},
	})
}

// R13: cold start and the calibration session.
func TestR13_ColdStart(t *testing.T) {
	runRuleTable(t, []ruleCase{
		{name: "usual weight is set 1", cfg: squat, cold: usual(60),
			want: want{phase: PhaseCalibration, weights: []float64{60, 62.5, 65}, reps: []int{12, 10, 8}, why: "Start at your usual weight: 60 kg"}},
		{name: "usual weight rounds down to the step", cfg: squat, cold: usual(61.2),
			want: want{phase: PhaseCalibration, weights: []float64{60, 62.5, 65}, why: "your usual 61.2 kg rounded down to the 2.5 kg step"}},
		{name: "usual weight below the empty bar starts at the bar", cfg: squat, cold: usual(15),
			want: want{phase: PhaseCalibration, weights: []float64{20, 22.5, 25}, why: "the lightest option"}},
		{name: "don't know: barbell starts at the empty bar", cfg: squat, cold: dontKnow,
			want: want{phase: PhaseCalibration, weights: []float64{20, 22.5, 25}, why: "Start light: 20 kg"}},
		{name: "don't know: dumbbell starts at 2.5", cfg: lateral, cold: dontKnow,
			want: want{phase: PhaseCalibration, weights: []float64{2.5, 5, 7.5}}},
		{name: "don't know: machine starts at one pin", cfg: calf, cold: dontKnow,
			want: want{phase: PhaseCalibration, weights: []float64{5, 10, 15}, reps: []int{18, 15, 12}}},
		{name: "don't know: cable starts at one pin", cfg: pulldown, cold: dontKnow,
			want: want{phase: PhaseCalibration, weights: []float64{5, 10, 15}}},
		{name: "all sets completed: heaviest is 65 but capped at one step (R14)", cfg: squat, cold: usual(60),
			steps: steps(reps(12, 10, 8)),
			want:  want{phase: PhaseBuilding, weights: []float64{62.5, 65, 67.5}, why: "+2.5 kg: first session, you completed set 3 at 65 kg"}},
		{name: "sets 1-2 completed: start from set 2's weight", cfg: squat, cold: usual(60),
			steps: steps(reps(12, 10, 7)),
			want:  want{phase: PhaseBuilding, weights: []float64{62.5, 65, 67.5}, why: "completed set 2 at 62.5 kg"}},
		{name: "only set 1 completed: stay", cfg: squat, cold: usual(60),
			steps: steps(reps(12, 9, 6)),
			want:  want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "Hold: set 1 at 60 kg was your heaviest completed set"}},
		{name: "13 but only set 1 completed: cold-start rule wins", cfg: squat, cold: usual(60),
			steps: steps(reps(13, 9, 6)),
			want:  want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}}},
		{name: "nothing completed, set 1 in the zone: normal hold", cfg: squat, cold: usual(60),
			steps: steps(reps(11, 9, 7)),
			want:  want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "Hold: 11 reps"}},
		{name: "nothing completed, set 1 failed: failure rule", cfg: squat, cold: usual(60),
			steps: steps(reps(8, 6, 5)),
			want:  want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}, why: "Hold: set 1 got 8, below 10"}},
	})

	// Once there is history, the cold-start answer is ignored.
	hist, _ := simulate(t, squat, usual(60), steady)
	assertPlan(t, mustNext(t, squat, hist, usual(100)), want{phase: PhaseNormal, weights: []float64{60, 62.5, 65}})
}

// R14: safety clamp.
func TestR14_Safety(t *testing.T) {
	r, _ := squat.rules()
	tests := []struct {
		next, lifted, want float64
	}{
		{70, 60, 62.5},
		{50, 60, 57.5},
		{61, 60, 61},
		{60, 60, 60},
	}
	for _, tt := range tests {
		if got := r.clamp(g(tt.next), g(tt.lifted)); got != g(tt.want) {
			t.Errorf("clamp(%v, %v) = %d g, want %v kg", tt.next, tt.lifted, got, tt.want)
		}
	}
	// End to end: whatever the user lifts, set 1 next time is within one step of it.
	_, p := simulate(t, squat, usual(60), steady, lifted(s(80, 15), s(82.5, 12), s(85, 10)))
	assertPlan(t, p, want{weights: []float64{82.5, 85, 87.5}})
}

// R15: invalid input is rejected.
func TestR15_Validation(t *testing.T) {
	valid, _ := simulate(t, squat, usual(60), steady, reps(12, 10, 8))
	mutate := func(f func(h []Session)) []Session {
		h := make([]Session, len(valid))
		for i, ss := range valid {
			ss.Done = slices.Clone(ss.Done)
			h[i] = ss
		}
		f(h)
		return h
	}
	tests := []struct {
		name    string
		cfg     Config
		hist    []Session
		cold    ColdStart
		wantErr error
	}{
		{"unknown equipment", Config{Equipment: "kettlebells", Size: Big, Movement: Compound, Scheme: Standard, RepStep: 2}, nil, usual(10), ErrConfig},
		{"unknown scheme", Config{Equipment: "barbell", Size: Big, Movement: Compound, Scheme: "pyramid", RepStep: 2}, nil, usual(10), ErrConfig},
		{"rep_step does not match scheme", Config{Equipment: "barbell", Size: Big, Movement: Compound, Scheme: Standard, RepStep: 3}, nil, usual(10), ErrConfig},
		{"unknown size", Config{Equipment: "barbell", Size: "medium", Movement: Compound, Scheme: Standard, RepStep: 2}, nil, usual(10), ErrConfig},
		{"unknown movement", Config{Equipment: "barbell", Size: Big, Scheme: Standard, RepStep: 2}, nil, usual(10), ErrConfig},
		{"cold start without weight or don't know", squat, nil, ColdStart{}, ErrInvalidInput},
		{"cold start with negative weight", squat, nil, usual(-5), ErrInvalidInput},
		{"weight 0", squat, mutate(func(h []Session) { h[1].Done[0].WeightG = 0 }), ColdStart{}, ErrInvalidInput},
		{"negative weight", squat, mutate(func(h []Session) { h[1].Done[2].WeightG = -2500 }), ColdStart{}, ErrInvalidInput},
		{"0 reps", squat, mutate(func(h []Session) { h[1].Done[1].Reps = 0 }), ColdStart{}, ErrInvalidInput},
		{"51 reps", squat, mutate(func(h []Session) { h[0].Done[0].Reps = 51 }), ColdStart{}, ErrInvalidInput},
		{"no sets logged", squat, mutate(func(h []Session) { h[1].Done = nil }), ColdStart{}, ErrInvalidInput},
		{"4 sets logged", squat, mutate(func(h []Session) { h[1].Done = append(h[1].Done, s(65, 8)) }), ColdStart{}, ErrInvalidInput},
		{"dates out of order", squat, mutate(func(h []Session) { h[1].Date = h[0].Date.AddDate(0, 0, -1) }), ColdStart{}, ErrInvalidInput},
		{"missing date", squat, mutate(func(h []Session) { h[0].Date = time.Time{} }), ColdStart{}, ErrInvalidInput},
		{"missing prescribed plan", squat, mutate(func(h []Session) { h[1].Plan = Plan{} }), ColdStart{}, ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Next(tt.cfg, tt.hist, tt.cold); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}

	// Boundaries that must be accepted: 1 and 50 reps, same-day sessions.
	ok := mutate(func(h []Session) {
		h[0].Done[0].Reps = 1
		h[1].Done[0].Reps = 50
		h[1].Date = h[0].Date
	})
	if _, err := Next(squat, ok, ColdStart{}); err != nil {
		t.Fatalf("boundary input rejected: %v", err)
	}
}

// R16: why formatting.
func TestR16_Why(t *testing.T) {
	for grams, want := range map[int]string{2500: "2.5", 5000: "5", 12500: "12.5", 62500: "62.5", 20000: "20", 1250: "1.25"} {
		if got := kg(grams); got != want {
			t.Errorf("kg(%d) = %q, want %q", grams, got, want)
		}
	}
	if got := joinInts([]int{15, 12, 9}); got != "15/12/9" {
		t.Errorf("joinInts = %q", got)
	}
	_, p := simulate(t, lateral, usual(5), steady, reps(13, 10, 8), lifted(s(7.5, 11), s(7.5, 9), s(7.5, 8)))
	if p.Sets[0].Why != p.Summary || p.Sets[1].Why != "One step (+2.5 kg) above set 1" || p.Sets[2].Why != "Same weight as set 2 until set 1 hits 12" {
		t.Errorf("per-set whys = %q / %q / %q", p.Sets[0].Why, p.Sets[1].Why, p.Sets[2].Why)
	}
}

// R17: deterministic, and inputs are not mutated.
func TestR17_Deterministic(t *testing.T) {
	hist, _ := simulate(t, lateral, usual(5), steady, reps(13, 10, 8), lifted(s(7.5, 11), s(7.5, 9), s(7.5, 8)))
	snapshot := make([]Session, len(hist))
	for i, ss := range hist {
		ss.Done = slices.Clone(ss.Done)
		ss.Plan.Sets = slices.Clone(ss.Plan.Sets)
		snapshot[i] = ss
	}
	a, errA := Next(lateral, hist, ColdStart{})
	b, errB := Next(lateral, hist, ColdStart{})
	if errA != nil || errB != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("two calls differ:\n%+v\n%+v", a, b)
	}
	if !reflect.DeepEqual(hist, snapshot) {
		t.Fatal("Next mutated its history input")
	}
}
