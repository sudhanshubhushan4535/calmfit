// Package progression is CalmFit's deterministic progression engine: given an exercise's config
// and its recent history it returns the next session's targets. Pure Go: no I/O, no clock, no LLM.
// The rules live in docs/progression.md (R1–R17); keep code, doc and tests in sync.
package progression

import (
	"errors"
	"fmt"
)

// Errors returned by the engine. ErrInvalidInput is the caller's/user's fault (map to 400);
// ErrConfig means the exercise config itself is wrong (a server-side bug).
var (
	ErrInvalidInput = errors.New("progression: invalid input")
	ErrConfig       = errors.New("progression: invalid exercise config")
)

// MuscleSize decides the post-increase rule (R8 big, R9 small).
type MuscleSize string

// Muscle sizes.
const (
	Big   MuscleSize = "big"
	Small MuscleSize = "small"
)

// Movement decides whether warm-ups are suggested (R4: compound only).
type Movement string

// Movement types.
const (
	Compound  Movement = "compound"
	Isolation Movement = "isolation"
)

// RepScheme picks the rep targets (R1).
type RepScheme string

// Rep schemes.
const (
	Standard RepScheme = "standard"
	HighRep  RepScheme = "high_rep"
)

// Limits (R1, R4, R15).
const (
	SetsPerExercise = 3
	MinReps         = 1
	MaxReps         = 50
	WarmupReps      = 2
)

// Config is one exercise's settings, as seeded in the exercises table.
type Config struct {
	ExerciseID string
	Equipment  string
	Size       MuscleSize
	Movement   Movement
	Scheme     RepScheme
	RepStep    int
}

// StepFor returns the weight step in grams for an equipment type (R2).
func StepFor(equipment string) (int, error) {
	switch equipment {
	case "barbell", "dumbbell":
		return 2500, nil
	case "machine", "cable":
		return 5000, nil
	}
	return 0, fmt.Errorf("%w: no weight step for equipment %q", ErrConfig, equipment)
}

// lightestFor is the "don't know" start and the floor for drops and warm-ups (R2).
func lightestFor(equipment string, step int) int {
	if equipment == "barbell" {
		return 20000 // empty Olympic bar
	}
	return step
}

func schemeParams(s RepScheme) (top, repStep int, err error) {
	switch s {
	case Standard:
		return 12, 2, nil
	case HighRep:
		return 18, 3, nil
	}
	return 0, 0, fmt.Errorf("%w: unknown rep scheme %q", ErrConfig, s)
}

// Targets returns the 3 per-set rep targets for a scheme (R1); shifted lowers them by repStep (R8, R12).
func Targets(s RepScheme, repStep int, shifted bool) ([]int, error) {
	top, want, err := schemeParams(s)
	if err != nil {
		return nil, err
	}
	if repStep != want {
		return nil, fmt.Errorf("%w: rep_step %d does not match %s scheme (want %d)", ErrConfig, repStep, s, want)
	}
	if shifted {
		top -= repStep
	}
	return []int{top, top - repStep, top - 2*repStep}, nil
}

// rules is a validated Config with its derived numbers.
type rules struct {
	cfg      Config
	top      int // normal set-1 target
	repStep  int
	step     int // weight step, grams
	lightest int // grams
}

func (c Config) rules() (rules, error) {
	top, _, err := schemeParams(c.Scheme)
	if err != nil {
		return rules{}, err
	}
	if _, err := Targets(c.Scheme, c.RepStep, false); err != nil {
		return rules{}, err
	}
	if c.Size != Big && c.Size != Small {
		return rules{}, fmt.Errorf("%w: unknown muscle size %q", ErrConfig, c.Size)
	}
	if c.Movement != Compound && c.Movement != Isolation {
		return rules{}, fmt.Errorf("%w: unknown movement type %q", ErrConfig, c.Movement)
	}
	step, err := StepFor(c.Equipment)
	if err != nil {
		return rules{}, err
	}
	return rules{cfg: c, top: top, repStep: c.RepStep, step: step, lightest: lightestFor(c.Equipment, step)}, nil
}

// targets is Targets for an already-validated config.
func (r rules) targets(shifted bool) []int {
	t, _ := Targets(r.cfg.Scheme, r.repStep, shifted)
	return t
}

// clamp keeps a new set-1 weight within one step of what was actually lifted (R14).
func (r rules) clamp(next, lifted int) int {
	return max(min(next, lifted+r.step), lifted-r.step)
}
