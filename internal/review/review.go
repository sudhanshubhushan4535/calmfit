// Package review builds the weekly review (Mon–Sun, local): workouts done vs planned, lifts that
// went up, lifts the engine flagged as stalled, the plate streak and one rule-based tip.
// Pure Go: the caller loads the data; there is no I/O, no clock and no LLM here.
package review

import (
	"fmt"
	"strings"
)

// Set is a set-1 result.
type Set struct {
	WeightG int `json:"weight_g"`
	Reps    int `json:"reps"`
}

// Lift is one exercise trained during the week.
type Lift struct {
	ExerciseID string
	Name       string
	Before     *Set  // last set 1 before the week; nil if none
	Week       []Set // set 1 of each session this week
	Stalled    bool  // the engine's plan after this week has Stalled set (R12)
}

// Input is everything the review needs.
type Input struct {
	Done, Planned int
	Lifts         []Lift
	PlateGoodDays int
	PlateDays     int // days of the week with any plate entry (empty days are neutral)
	PlateStreak   int
}

// Change is a lift that went up: best set 1 this week vs the last set 1 before it.
type Change struct {
	ExerciseID string `json:"exercise_id"`
	Name       string `json:"name"`
	From       Set    `json:"from"`
	To         Set    `json:"to"`
}

// Stalled is a lift the engine flagged as stalled.
type Stalled struct {
	ExerciseID string `json:"exercise_id"`
	Name       string `json:"name"`
}

// Plate is the week's plate-check summary.
type Plate struct {
	GoodDays int `json:"good_days"`
	Days     int `json:"days"` // logged days; days with no entries are left out
	Streak   int `json:"streak"`
}

// Review is the weekly review.
type Review struct {
	Done    int       `json:"done"`
	Planned int       `json:"planned"`
	WentUp  []Change  `json:"went_up"`
	Stalled []Stalled `json:"stalled"`
	Plate   Plate     `json:"plate"`
	Tip     string    `json:"tip"`
}

// better reports whether a beats b: more weight, or the same weight for more reps.
func better(a, b Set) bool {
	return a.WeightG > b.WeightG || (a.WeightG == b.WeightG && a.Reps > b.Reps)
}

// Best returns the week's best set 1 (more weight wins, then more reps).
func Best(sets []Set) Set {
	var best Set
	for _, s := range sets {
		if better(s, best) {
			best = s
		}
	}
	return best
}

// Build assembles the review. A lift with no set 1 before the week can't have "gone up".
func Build(in Input) Review {
	r := Review{Done: in.Done, Planned: in.Planned, WentUp: []Change{}, Stalled: []Stalled{},
		Plate: Plate{GoodDays: in.PlateGoodDays, Days: in.PlateDays, Streak: in.PlateStreak}}
	for _, l := range in.Lifts {
		if len(l.Week) > 0 && l.Before != nil {
			if best := Best(l.Week); better(best, *l.Before) {
				r.WentUp = append(r.WentUp, Change{ExerciseID: l.ExerciseID, Name: l.Name, From: *l.Before, To: best})
			}
		}
		if l.Stalled {
			r.Stalled = append(r.Stalled, Stalled{ExerciseID: l.ExerciseID, Name: l.Name})
		}
	}
	r.Tip = Tip(r)
	return r
}

// Tip picks one tip; the first matching rule wins:
//  1. no workouts done → start with one session
//  2. fewer done than planned → consistency
//  3. any stalled lift → the plan has adjusted it; clean reps, sleep, food
//  4. good plate days on fewer than half the logged days → protein (no logged days → no plate tip)
//  5. otherwise → keep the same routine
func Tip(r Review) string {
	switch {
	case r.Done == 0 && r.Planned > 0:
		return "No workouts logged this week. Start with one session; your plan picks up where you left off."
	case r.Done < r.Planned:
		return fmt.Sprintf("You did %d of %d planned workouts. Showing up matters more than any single session: "+
			"aim for all %d next week.", r.Done, r.Planned, r.Planned)
	case len(r.Stalled) > 0:
		names := make([]string, len(r.Stalled))
		for i, s := range r.Stalled {
			names[i] = s.Name
		}
		return fmt.Sprintf("%s stalled. The plan has already adjusted the targets: focus on clean reps, sleep and protein.",
			strings.Join(names, ", "))
	case r.Plate.Days > 0 && r.Plate.GoodDays*2 < r.Plate.Days:
		return fmt.Sprintf("Good plate days: %d of %d logged. One easy win: add a protein-rich serving to one more meal a day.",
			r.Plate.GoodDays, r.Plate.Days)
	default:
		return "Solid week. Keep the same routine next week."
	}
}
