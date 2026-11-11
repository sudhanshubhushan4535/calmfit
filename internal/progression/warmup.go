package progression

// warmups suggests ~50% and ~75% of set 1, rounded to the step, for compound exercises only (R4).
func (r rules) warmups(set1 int) []Set {
	out := []Set{}
	if r.cfg.Movement != Compound {
		return out
	}
	for _, pct := range []int{50, 75} {
		w := max(r.roundToStep(set1*pct/100), r.lightest)
		if w >= set1 || (len(out) > 0 && out[len(out)-1].WeightG == w) {
			continue
		}
		out = append(out, Set{WeightG: w, Reps: WarmupReps})
	}
	return out
}

// roundToStep rounds grams to the nearest multiple of the step; ties round down.
func (r rules) roundToStep(g int) int {
	q, rem := g/r.step, g%r.step
	if rem*2 > r.step {
		q++
	}
	return q * r.step
}
