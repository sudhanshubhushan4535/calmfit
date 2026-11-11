package progression

import (
	"fmt"
	"time"
)

// Phase is where an exercise is in the progression cycle (R17).
type Phase string

// Phases.
const (
	PhaseCalibration Phase = "calibration" // first session after a cold start (R13)
	PhaseNormal      Phase = "normal"
	PhaseBuilding    Phase = "building" // after an increase (R5)
	PhaseShifted     Phase = "shifted"  // targets lowered by rep_step (R8, R12)
	PhaseCapped      Phase = "capped"   // small muscles: set 3 at set 2's weight (R9)
)

func (p Phase) valid() bool {
	switch p {
	case PhaseCalibration, PhaseNormal, PhaseBuilding, PhaseShifted, PhaseCapped:
		return true
	}
	return false
}

// Set is one logged set (weights in grams).
type Set struct {
	WeightG int `json:"weight_g"`
	Reps    int `json:"reps"`
}

// Target is one prescribed set with its one-line reason (R16).
type Target struct {
	WeightG int    `json:"weight_g"`
	Reps    int    `json:"reps"`
	Why     string `json:"why"`
}

// Plan is the engine's output for one session. It is also stored with the logged session as the
// snapshot of what was prescribed (R17), which is where the streak counters live.
type Plan struct {
	Phase   Phase    `json:"phase"`
	Sets    []Target `json:"sets"`
	Warmups []Set    `json:"warmups"`
	Summary string   `json:"summary"`
	Stalled bool     `json:"stalled"`
	// FailStreak counts consecutive failed sessions before this one (R7).
	FailStreak int `json:"fail_streak"`
	// NoProgress counts consecutive sessions without progress before this one (R12).
	NoProgress int `json:"no_progress"`
}

// Session is one logged session of this exercise: what was prescribed and what was done.
type Session struct {
	Date time.Time `json:"date"`
	Plan Plan      `json:"plan"`
	Done []Set     `json:"done"`
}

// ColdStart is the user's answer when an exercise has no history (R13).
type ColdStart struct {
	UsualWeightG int  // the user's usual set-1 weight, grams
	DontKnow     bool // start at the lightest option instead
}

// Next returns the targets for the next session. history is this exercise's logged sessions in
// date order (only the last two are read); cold is used only when history is empty.
func Next(cfg Config, history []Session, cold ColdStart) (Plan, error) {
	r, err := cfg.rules()
	if err != nil {
		return Plan{}, err
	}
	if err := validateHistory(history); err != nil {
		return Plan{}, err
	}
	var d decision
	if len(history) == 0 {
		if d, err = r.coldStart(cold); err != nil {
			return Plan{}, err
		}
	} else {
		last := history[len(history)-1]
		var prev *Session
		if len(history) >= 2 {
			prev = &history[len(history)-2]
		}
		d = r.decide(last, prev)
	}
	return r.build(d), nil
}

type decision struct {
	weight     int
	phase      Phase
	summary    string
	stalled    bool
	failStreak int
	noProgress int
}

func (r rules) coldStart(c ColdStart) (decision, error) {
	if c.DontKnow {
		return decision{weight: r.lightest, phase: PhaseCalibration,
			summary: fmt.Sprintf("Start light: %s kg is the lightest option", kg(r.lightest))}, nil
	}
	if c.UsualWeightG <= 0 {
		return decision{}, fmt.Errorf("%w: cold start needs a usual weight > 0 or \"don't know\"", ErrInvalidInput)
	}
	w := c.UsualWeightG / r.step * r.step
	why := fmt.Sprintf("Start at your usual weight: %s kg", kg(w))
	switch {
	case w < r.lightest:
		w = r.lightest
		why = fmt.Sprintf("Start at %s kg: the lightest option", kg(w))
	case w != c.UsualWeightG:
		why = fmt.Sprintf("Start at %s kg: your usual %s kg rounded down to the %s kg step", kg(w), kg(c.UsualWeightG), kg(r.step))
	}
	return decision{weight: w, phase: PhaseCalibration, summary: why}, nil
}

// decide applies R5–R14 to the last logged session. Order follows R16's priority.
func (r rules) decide(last Session, prev *Session) decision {
	w := last.Done[0].WeightG // logged weight is the truth (R14)
	reps1 := last.Done[0].Reps
	floor := r.top - r.repStep // bottom of the hold zone; below it is a failure (R7)
	failed := reps1 < floor

	failStreak := 0
	if failed {
		failStreak = last.Plan.FailStreak + 1
	}
	noProgress := 0
	if prev != nil && !progressed(last, *prev) {
		noProgress = last.Plan.NoProgress + 1
	}

	// hold keeps the weight and the counters; change resets the no-progress counter (R12).
	hold := func(phase Phase, why string) decision {
		return decision{weight: w, phase: phase, summary: why, failStreak: failStreak, noProgress: noProgress}
	}
	change := func(next int, phase Phase, why string) decision {
		return decision{weight: r.clamp(next, w), phase: phase, summary: why, failStreak: failStreak}
	}
	drop := func(reason string, stalled bool) decision {
		next := w - r.step
		if next < r.lightest {
			next = min(w, r.lightest) // never below the lightest option (R7)
		}
		why := fmt.Sprintf("-%s kg: %s", kg(w-next), reason)
		if next == w {
			why = fmt.Sprintf("Hold at %s kg, the lightest option: %s", kg(w), reason)
		}
		return decision{weight: next, phase: PhaseNormal, summary: why, stalled: stalled}
	}

	phase := last.Plan.Phase

	// R13: after calibration, start from the heaviest completed set, at most one step up (R14).
	if phase == PhaseCalibration {
		heaviest, setNo := 0, 0
		for i, s := range last.Done {
			if s.Reps >= last.Plan.Sets[i].Reps && s.WeightG > heaviest {
				heaviest, setNo = s.WeightG, i+1
			}
		}
		if heaviest > 0 {
			next := r.clamp(heaviest, w)
			switch {
			case next > w:
				return change(next, PhaseBuilding, fmt.Sprintf("+%s kg: first session, you completed set %d at %s kg", kg(next-w), setNo, kg(heaviest)))
			case next < w:
				return change(next, PhaseNormal, fmt.Sprintf("-%s kg: first session, your heaviest completed set was %s kg", kg(w-next), kg(heaviest)))
			}
			return change(w, PhaseNormal, fmt.Sprintf("Hold: set %d at %s kg was your heaviest completed set", setNo, kg(heaviest)))
		}
		phase = PhaseNormal // nothing completed: normal rules apply
	}

	// R12: stall.
	if noProgress >= 3 {
		short := shortfall(last)
		switch {
		case short == 0:
			d := hold(phase, fmt.Sprintf("Stalled at %d reps: push set 1 for %d", reps1, r.top+1))
			d.stalled, d.noProgress = true, 0
			return d
		case short <= 2:
			d := change(w, PhaseShifted, fmt.Sprintf("Stalled 3 sessions, %s short: same weight, targets %s", plural(short, "rep"), joinInts(r.targets(true))))
			d.stalled = true
			return d
		default:
			return drop(fmt.Sprintf("stalled 3 sessions, %s short", plural(short, "rep")), true)
		}
	}

	// R7: failure.
	if failed {
		if failStreak >= 2 {
			return drop(fmt.Sprintf("set 1 was under %d reps two sessions in a row", floor), false)
		}
		return hold(phase, fmt.Sprintf("Hold: set 1 got %d, below %d", reps1, floor))
	}

	// R8/R9: build back before any increase.
	switch phase {
	case PhaseShifted:
		if reps1 >= r.top {
			return change(w, PhaseNormal, fmt.Sprintf("Same weight, back to %s: set 1 hit %d", joinInts(r.targets(false)), reps1))
		}
		return hold(PhaseShifted, fmt.Sprintf("Hold: build set 1 back to %d at this weight", r.top))
	case PhaseCapped:
		if reps1 >= r.top {
			return change(w, PhaseNormal, fmt.Sprintf("Set 3 back to its full weight: set 1 hit %d", reps1))
		}
		return hold(PhaseCapped, fmt.Sprintf("Hold: set 3 stays at set 2's weight until set 1 hits %d", r.top))
	}

	// R5: increase.
	if reps1 > r.top {
		return change(w+r.step, PhaseBuilding, fmt.Sprintf("+%s kg: you hit %d reps last time", kg(r.step), reps1))
	}

	holdWhy := fmt.Sprintf("Hold: %d reps on set 1; hit %d to go up", reps1, r.top+1)
	if phase == PhaseBuilding {
		switch {
		case r.cfg.Size == Big && reps1 == floor: // R8
			return change(w, PhaseShifted, fmt.Sprintf("Same weight, targets %s: set 1 reached only %d after the increase", joinInts(r.targets(true)), reps1))
		case r.cfg.Size == Small && reps1 < r.top && set2Missed(last): // R9 (A17b): only while set 1 is below the top
			return change(w, PhaseCapped, "Same weights, but set 3 stays at set 2's weight: set 2 missed after the increase")
		case reps1 < r.top:
			return hold(PhaseBuilding, holdWhy)
		}
	}
	return hold(PhaseNormal, holdWhy) // R6
}

// progressed reports whether cur beat prev (R11): heavier set 1, or same set 1 and more total reps.
func progressed(cur, prev Session) bool {
	cw, pw := cur.Done[0].WeightG, prev.Done[0].WeightG
	if cw != pw {
		return cw > pw
	}
	return totalReps(cur.Done) > totalReps(prev.Done)
}

func totalReps(sets []Set) int {
	n := 0
	for _, s := range sets {
		n += s.Reps
	}
	return n
}

// shortfall is the reps missed against the prescribed targets; unlogged sets count as 0 (R12).
func shortfall(s Session) int {
	short := 0
	for i, t := range s.Plan.Sets {
		done := 0
		if i < len(s.Done) {
			done = s.Done[i].Reps
		}
		short += max(0, t.Reps-done)
	}
	return short
}

func set2Missed(s Session) bool {
	return len(s.Done) < 2 || s.Done[1].Reps < s.Plan.Sets[1].Reps
}

// build turns a decision into per-set targets, warm-ups and reasons (R3, R4, R9, R16).
func (r rules) build(d decision) Plan {
	reps := r.targets(d.phase == PhaseShifted)
	weights := []int{d.weight, d.weight + r.step, d.weight + 2*r.step}
	if d.phase == PhaseCapped {
		weights[2], reps[2] = weights[1], reps[1]
	}
	sets := make([]Target, SetsPerExercise)
	for i := range sets {
		why := d.summary
		switch {
		case i == 0:
		case d.phase == PhaseCapped && i == 2:
			why = fmt.Sprintf("Same weight as set 2 until set 1 hits %d", r.top)
		default:
			why = fmt.Sprintf("One step (+%s kg) above set %d", kg(r.step), i)
		}
		sets[i] = Target{WeightG: weights[i], Reps: reps[i], Why: why}
	}
	return Plan{
		Phase:      d.phase,
		Sets:       sets,
		Warmups:    r.warmups(d.weight),
		Summary:    d.summary,
		Stalled:    d.stalled,
		FailStreak: d.failStreak,
		NoProgress: d.noProgress,
	}
}

func validateSet(where string, s Set) error {
	if s.WeightG <= 0 {
		return fmt.Errorf("%w: %s: weight must be > 0, got %d g", ErrInvalidInput, where, s.WeightG)
	}
	if s.Reps < MinReps || s.Reps > MaxReps {
		return fmt.Errorf("%w: %s: reps must be %d–%d, got %d", ErrInvalidInput, where, MinReps, MaxReps, s.Reps)
	}
	return nil
}

func validateHistory(h []Session) error {
	for i, s := range h {
		n := i + 1
		if s.Date.IsZero() {
			return fmt.Errorf("%w: session %d has no date", ErrInvalidInput, n)
		}
		if i > 0 && s.Date.Before(h[i-1].Date) {
			return fmt.Errorf("%w: session %d is dated before session %d", ErrInvalidInput, n, i)
		}
		if len(s.Plan.Sets) != SetsPerExercise || !s.Plan.Phase.valid() {
			return fmt.Errorf("%w: session %d has no valid prescribed plan", ErrInvalidInput, n)
		}
		if len(s.Done) < 1 || len(s.Done) > SetsPerExercise {
			return fmt.Errorf("%w: session %d must log 1–%d sets, got %d", ErrInvalidInput, n, SetsPerExercise, len(s.Done))
		}
		for j, set := range s.Done {
			if err := validateSet(fmt.Sprintf("session %d set %d", n, j+1), set); err != nil {
				return err
			}
		}
	}
	return nil
}
