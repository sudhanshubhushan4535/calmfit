package seed

import (
	"errors"
	"fmt"
	"slices"
)

// ValidateExercise checks one catalog entry's own fields.
func ValidateExercise(e Exercise) error {
	if e.WeightStepG <= 0 {
		return fmt.Errorf("exercise %s: weight_step_g must be > 0, got %d", e.ID, e.WeightStepG)
	}
	return nil
}

// Validate checks a template against the catalog and returns every violation joined.
func Validate(t Template, catalog map[string]Exercise) error {
	var errs []error

	type key struct{ day, muscle, mtype string }
	seen := map[key]string{} // -> first exercise id holding that slot

	for _, te := range t.Exercises {
		if !slices.Contains(t.Days, te.Day) {
			errs = append(errs, fmt.Errorf("%s: exercise %s has day %q not in %v", t.ID, te.ExerciseID, te.Day, t.Days))
		}
		ex, ok := catalog[te.ExerciseID]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: exercise %s (day %q) is not in the exercise catalog", t.ID, te.ExerciseID, te.Day))
			continue
		}
		k := key{te.Day, ex.TargetMuscle, ex.MovementType}
		if prev, dup := seen[k]; dup {
			errs = append(errs, fmt.Errorf("%s: day %q has two %s %s exercises: %s and %s",
				t.ID, te.Day, ex.MovementType, ex.TargetMuscle, prev, te.ExerciseID))
			continue
		}
		seen[k] = te.ExerciseID
	}
	return errors.Join(errs...)
}

// MuscleGroup maps a major muscle group to the fine-grained target_muscle tags that count for it.
type MuscleGroup struct {
	Name string
	Tags []string
}

// MajorMuscles is the coverage checklist, in report order.
var MajorMuscles = []MuscleGroup{
	{"chest", []string{"upper_chest", "mid_chest"}},
	{"shoulders", []string{"front_delt", "side_delt", "rear_delt"}},
	{"back", []string{"lats", "mid_back", "upper_traps"}},
	{"biceps", []string{"biceps", "brachialis"}},
	{"triceps", []string{"triceps"}},
	{"quads", []string{"quads"}},
	{"hamstrings", []string{"hamstrings"}},
	{"glutes", []string{"glutes"}},
	{"calves", []string{"calves"}},
	{"core", []string{"abs", "lower_back"}},
}

// Coverage returns the major muscle groups no exercise in the template targets across the cycle.
func Coverage(t Template, catalog map[string]Exercise) []string {
	hit := map[string]bool{}
	for _, te := range t.Exercises {
		if ex, ok := catalog[te.ExerciseID]; ok {
			hit[ex.TargetMuscle] = true
		}
	}
	var missing []string
	for _, g := range MajorMuscles {
		if !slices.ContainsFunc(g.Tags, func(tag string) bool { return hit[tag] }) {
			missing = append(missing, g.Name)
		}
	}
	return missing
}
