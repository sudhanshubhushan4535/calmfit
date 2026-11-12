// Package voice turns a spoken set description ("incline 20, 12 10 8") into sets the user can
// confirm. A Parser (the LLM) extracts what was said; Resolve checks it against today's workout
// and fills in set numbers and weights. Nothing here writes: confirmed sets are saved by the
// client through the normal set-logging endpoint, so voice has no write path of its own.
package voice

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
)

// Unit is the weight unit the user said.
type Unit string

const (
	KG Unit = "kg"
	LB Unit = "lb"
)

// gramsPerLB is the exact international pound.
const gramsPerLB = 453.59237

// Weight sources on a resolved set.
const (
	SourceSaid = "said" // spoken for this set, or carried forward from an earlier set
	SourcePlan = "plan" // nothing said yet: the plan's target for this set
)

// Parsed is what a Parser extracted, before any business validation.
type Parsed struct {
	// ExerciseID is "" when no exercise was named, or the one named isn't in the list.
	ExerciseID string `json:"exercise_id"`
	// OtherExercise is true when an exercise was named that isn't in today's workout. The open
	// exercise must then not stand in for it.
	OtherExercise bool `json:"other_exercise"`
	// Unit is "" when no weight was said; it then defaults to kg.
	Unit Unit        `json:"unit"`
	Sets []ParsedSet `json:"sets"`
}

// ParsedSet is one set as said. Weight is nil when no weight was said for it.
type ParsedSet struct {
	Weight *float64 `json:"weight"`
	Reps   int      `json:"reps"`
}

// Candidate is an exercise the user may mean: one in today's workout.
type Candidate struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

// Parser extracts sets from a transcript. It is the only part that talks to an LLM; it never
// sees who the user is. Implementations return ErrCouldNotParse when the text isn't a set log.
type Parser interface {
	Parse(ctx context.Context, transcript string, candidates []Candidate) (Parsed, error)
}

// ErrCouldNotParse means the transcript held no usable set description.
var ErrCouldNotParse = &Error{Code: CodeCouldNotParse, Msg: "couldn't understand that; try e.g. \"incline 20, 12 10 8\""}

// Error codes, returned to the client as {"error": code}.
const (
	CodeCouldNotParse    = "could_not_parse"
	CodeNoExercise       = "no_exercise"
	CodeNotInWorkout     = "exercise_not_in_workout"
	CodeNeedsUsualWeight = "needs_usual_weight"
	CodeNoSets           = "no_sets"
	CodeTooManySets      = "too_many_sets"
	CodeInvalidWeight    = "invalid_weight"
	CodeInvalidReps      = "invalid_reps"
	CodeInvalidUnit      = "invalid_unit"
)

// Error is a parse the user has to fix (say it again, or edit it); the API maps it to 422.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return "voice: " + e.Code + ": " + e.Msg }

// IsUserError reports whether err is an *Error and returns it.
func IsUserError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Exercise is the state of one exercise in today's open workout.
type Exercise struct {
	ID string
	// NeedsUsualWeight is true while the exercise has no plan (cold start not answered yet).
	NeedsUsualWeight bool
	// Logged is how many sets are already logged; they are always sets 1..Logged.
	Logged int
	// Remaining is the targets for sets Logged+1.. (after in-session adjustment).
	Remaining []progression.Target
}

// Set is one resolved set, ready to show on the confirm screen and send to POST /workouts/{id}/sets.
type Set struct {
	SetNumber    int    `json:"set_number"`
	WeightG      int    `json:"weight_g"`
	Reps         int    `json:"reps"`
	WeightSource string `json:"weight_source"`
}

// Result is a resolved parse.
type Result struct {
	ExerciseID string `json:"exercise_id"`
	// UsualWeightG is set when the exercise still needs its usual-weight answer and the user said
	// a weight for set 1 (D31): on confirm, the client first answers the usual-weight question with
	// it (POST .../cold-start), then logs the sets. nil when no answer is needed.
	UsualWeightG *int     `json:"usual_weight_g"`
	Sets         []Set    `json:"sets"`
	Warnings     []string `json:"warnings"`
}

// Resolve validates a parse against today's workout. hint is the exercise the user has open
// ("" for none); it is used only when no exercise was named at all.
//
// Sets are numbered from the first unlogged set. Weights follow D31: a set with no weight said
// takes the last weight said before it in the same phrase ("bench 60, 8 8 7" logs all three at
// 60); a set before any weight was said takes the plan's target for that set.
//
// An exercise still waiting for its usual-weight answer has no plan. If set 1's weight was said,
// it becomes the proposed usual weight (Result.UsualWeightG) and the sets are filled from what was
// said; with no weight for set 1 there is nothing to fill it from, so the answer is
// needs_usual_weight.
func Resolve(p Parsed, workout []Exercise, hint string) (Result, error) {
	var res Result
	res.Warnings = []string{}

	if p.OtherExercise {
		return Result{}, &Error{CodeNotInWorkout, "that exercise isn't in today's workout"}
	}
	id := p.ExerciseID
	if id == "" {
		if hint == "" {
			return Result{}, &Error{CodeNoExercise, "say which exercise, or open it first"}
		}
		id = hint
		res.Warnings = append(res.Warnings, "exercise taken from the open screen")
	}
	ex, ok := find(workout, id)
	if !ok {
		return Result{}, &Error{CodeNotInWorkout, fmt.Sprintf("%q isn't in today's workout", id)}
	}
	res.ExerciseID = ex.ID

	if len(p.Sets) == 0 {
		return Result{}, &Error{CodeNoSets, "no sets heard"}
	}
	if ex.NeedsUsualWeight {
		if p.Sets[0].Weight == nil {
			return Result{}, &Error{CodeNeedsUsualWeight, "say the weight for set 1, or answer the usual-weight question first"}
		}
		if ex.Logged != 0 {
			// Set logging requires a plan, so an exercise without one can't have sets.
			return Result{}, fmt.Errorf("voice: exercise %s has %d sets but no plan", ex.ID, ex.Logged)
		}
	}
	left := progression.SetsPerExercise - ex.Logged
	if len(p.Sets) > left {
		return Result{}, &Error{CodeTooManySets, fmt.Sprintf("%d sets heard but only %d left to log", len(p.Sets), left)}
	}
	if !ex.NeedsUsualWeight && len(ex.Remaining) < len(p.Sets) {
		return Result{}, fmt.Errorf("voice: exercise %s has %d remaining targets for %d sets", ex.ID, len(ex.Remaining), len(p.Sets))
	}

	unit := p.Unit
	switch unit {
	case "":
		unit = KG
	case KG, LB:
	default:
		return Result{}, &Error{CodeInvalidUnit, fmt.Sprintf("unknown unit %q", p.Unit)}
	}

	var carried *int // grams of the last weight said
	for i, s := range p.Sets {
		if s.Reps < progression.MinReps || s.Reps > progression.MaxReps {
			return Result{}, &Error{CodeInvalidReps, fmt.Sprintf("reps must be %d–%d, heard %d", progression.MinReps, progression.MaxReps, s.Reps)}
		}
		set := Set{SetNumber: ex.Logged + 1 + i, Reps: s.Reps}
		switch {
		case s.Weight != nil:
			g, err := grams(*s.Weight, unit)
			if err != nil {
				return Result{}, err
			}
			carried = &g
			set.WeightG, set.WeightSource = g, SourceSaid
		case carried != nil:
			set.WeightG, set.WeightSource = *carried, SourceSaid
		default:
			set.WeightG, set.WeightSource = ex.Remaining[i].WeightG, SourcePlan
		}
		res.Sets = append(res.Sets, set)
	}
	if unit == LB && carried != nil {
		res.Warnings = append(res.Warnings, "converted from lb to kg (rounded to 0.1 kg)")
	}
	if ex.NeedsUsualWeight {
		// Set 1 always has a said weight here (checked above), so it is never a plan weight.
		usual := res.Sets[0].WeightG
		res.UsualWeightG = &usual
		res.Warnings = append(res.Warnings, "first time on this exercise: set 1's weight becomes your usual weight")
	}
	return res, nil
}

func find(workout []Exercise, id string) (Exercise, bool) {
	for _, x := range workout {
		if x.ID == id {
			return x, true
		}
	}
	return Exercise{}, false
}

// grams converts a spoken weight to grams: kg exactly to the gram, lb rounded to 0.1 kg.
func grams(w float64, unit Unit) (int, error) {
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
		return 0, &Error{CodeInvalidWeight, "weight must be more than 0"}
	}
	var g float64
	if unit == LB {
		g = math.Round(w*gramsPerLB/100) * 100
	} else {
		g = math.Round(w * 1000)
	}
	if g <= 0 || g > progression.MaxWeightG {
		return 0, &Error{CodeInvalidWeight, fmt.Sprintf("weight must be more than 0 and at most %d kg", progression.MaxWeightG/1000)}
	}
	return int(g), nil
}
