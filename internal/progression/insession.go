package progression

import (
	"fmt"
	"slices"
)

// AdjustInSession returns the targets for the sets not yet done, given the sets done so far (R10).
// Small muscles only: a missed set makes the next set repeat its weight and target reps, and later
// sets still rise at most one step per set (R14). Big muscles get the plan unchanged.
func AdjustInSession(cfg Config, plan Plan, done []Set) ([]Target, error) {
	r, err := cfg.rules()
	if err != nil {
		return nil, err
	}
	if len(plan.Sets) != SetsPerExercise {
		return nil, fmt.Errorf("%w: plan must have %d sets, got %d", ErrInvalidInput, SetsPerExercise, len(plan.Sets))
	}
	if len(done) > len(plan.Sets) {
		return nil, fmt.Errorf("%w: %d sets done but only %d planned", ErrInvalidInput, len(done), len(plan.Sets))
	}
	for i, s := range done {
		if err := validateSet(fmt.Sprintf("set %d", i+1), s); err != nil {
			return nil, err
		}
	}

	cur := slices.Clone(plan.Sets)
	if cfg.Size == Small {
		for k := 1; k < len(cur); k++ {
			prevDone := k-1 < len(done)
			prevW := cur[k-1].WeightG
			if prevDone {
				prevW = done[k-1].WeightG // logged weight is the truth
			}
			switch {
			case prevDone && done[k-1].Reps < cur[k-1].Reps:
				cur[k] = Target{WeightG: prevW, Reps: cur[k-1].Reps, Why: fmt.Sprintf("Hold: set %d missed its reps", k)}
			case cur[k].WeightG > prevW+r.step:
				cur[k].WeightG = prevW + r.step
				cur[k].Why = fmt.Sprintf("One step (+%s kg) above set %d", kg(r.step), k)
			}
		}
	}
	return cur[len(done):], nil
}
