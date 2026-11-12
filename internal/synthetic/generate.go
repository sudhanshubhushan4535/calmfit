// Package synthetic generates realistic training history for fake users, for load tests (D40, D41).
//
// Generation is pure and deterministic: user i is built from its own PCG stream seeded by
// (Seed, i), so the same Params always give the same rows, and users can be generated in any order.
// Every plan comes from the real engine (progression.Next and AdjustInSession), so the history is
// valid input to the API's own engine calls.
package synthetic

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/program"
	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
)

// SubPrefix starts every synthetic user's google_sub: "synthetic-00042". It matches the subs that
// test auth accepts (D39).
const SubPrefix = "synthetic-"

// SubPattern is the SQL regex for generated subs (exactly 5 digits), so Reset never touches
// anything else.
const SubPattern = `^synthetic-[0-9]{5}$`

// MaxUsers keeps subs at 5 digits.
const MaxUsers = 99999

// Sub returns user i's google_sub.
func Sub(i int) string { return fmt.Sprintf("%s%05d", SubPrefix, i) }

// Params fixes everything the output depends on.
type Params struct {
	Users  int
	Seed   uint64
	Anchor time.Time      // a Date (midnight UTC of a local day): history ends the day before
	Loc    *time.Location // the app timezone, for local workout and plate times
}

// Template is the program the users follow.
type Template struct {
	ID        string
	Days      []string                        // rotation order
	Exercises map[string][]progression.Config // per day, in template order
}

// User is one generated user and all their rows.
type User struct {
	Index              int
	ID                 [16]byte
	Sub, Email, Name   string
	CreatedAt          time.Time
	DaysPerWeek        int
	LastCompletedIndex int
	ProgramUpdatedAt   time.Time
	Workouts           []Workout
	Plates             []PlateCheck
}

// Workout is one completed workout.
type Workout struct {
	ID                     [16]byte
	Day                    string
	DayIndex               int
	StartedAt, CompletedAt time.Time
	Exercises              []WorkoutExercise
}

// WorkoutExercise is one exercise of a workout with the plan snapshot it was given.
type WorkoutExercise struct {
	ExerciseID string
	Position   int
	Plan       progression.Plan
	Sets       []Set // empty when skipped
}

// Set is one logged working set.
type Set struct {
	Number, WeightG, Reps int
	LoggedAt              time.Time
}

// PlateCheck is one meal's plate check.
type PlateCheck struct {
	Date           time.Time // a Date
	Meal           string
	Protein, Fiber bool
	UpdatedAt      time.Time
}

var meals = []string{"breakfast", "lunch", "dinner"}

// exState is one user's running state for one exercise.
type exState struct {
	e1rmKg   float64               // estimated one-rep max, the "true" strength
	hist     []progression.Session // last two logged sessions (all the engine reads)
	sessions int
}

// Generate builds user i (0-based).
func Generate(p Params, t Template, i int) (User, error) {
	rng := rand.New(rand.NewPCG(p.Seed, uint64(i)))
	u := User{Index: i, ID: uuid(rng), Sub: Sub(i)}
	u.Email = u.Sub + "@synthetic.invalid"
	u.Name = fmt.Sprintf("Synthetic %05d", i)

	// History length mix (D41): 40% 1–3 months, 40% 3–9 months, 20% 1–2 years (the heavy users).
	var days int
	switch r := rng.Float64(); {
	case r < 0.4:
		days = 30 + rng.IntN(61)
	case r < 0.8:
		days = 90 + rng.IntN(181)
	default:
		days = 365 + rng.IntN(366)
	}
	start := p.Anchor.AddDate(0, 0, -days)
	u.CreatedAt = localTime(start.AddDate(0, 0, -1-rng.IntN(3)), 20, rng.IntN(60), p.Loc)
	u.ProgramUpdatedAt = u.CreatedAt

	u.DaysPerWeek = 3 + rng.IntN(3)
	gymDay := [7]bool{}
	for _, wd := range rng.Perm(7)[:u.DaysPerWeek] {
		gymDay[wd] = true
	}
	attendance := 0.75 + 0.2*rng.Float64()
	level := rng.Float64()
	hour := 6 + rng.IntN(15) // usual training hour, 06–20

	states := map[string]*exState{}
	last := -1
	for d := start; d.Before(p.Anchor); d = d.AddDate(0, 0, 1) {
		if !gymDay[d.Weekday()] || rng.Float64() >= attendance {
			continue
		}
		idx := program.NextDayIndex(last, len(t.Days))
		day := t.Days[idx]
		h := hour + rng.IntN(3) - 1
		started := localTime(d, max(h, 5), rng.IntN(60), p.Loc)
		completed := started.Add(time.Duration(50+rng.IntN(26)) * time.Minute)
		w := Workout{ID: uuid(rng), Day: day, DayIndex: idx, StartedAt: started, CompletedAt: completed}

		cfgs := t.Exercises[day]
		slot := completed.Sub(started) / time.Duration(len(cfgs)*3+1)
		for pos, cfg := range cfgs {
			st := states[cfg.ExerciseID]
			if st == nil {
				st = &exState{e1rmKg: startingE1RM(cfg, level, rng)}
				states[cfg.ExerciseID] = st
			}
			var cold progression.ColdStart
			if len(st.hist) == 0 {
				if rng.Float64() < 0.2 {
					cold.DontKnow = true
				} else {
					// The weight they'd lift for ~12 reps (Epley: 1RM = w·(1 + reps/30)).
					cold.UsualWeightG = int(st.e1rmKg / 1.4 * 1000)
				}
			}
			plan, err := progression.Next(cfg, st.hist, cold)
			if err != nil {
				return User{}, fmt.Errorf("user %d %s: %w", i, cfg.ExerciseID, err)
			}
			we := WorkoutExercise{ExerciseID: cfg.ExerciseID, Position: pos, Plan: plan, Sets: []Set{}}
			if rng.Float64() < 0.03 { // skipped: planned, nothing logged
				w.Exercises = append(w.Exercises, we)
				continue
			}
			n := progression.SetsPerExercise
			if rng.Float64() < 0.03 {
				n = 1 + rng.IntN(2) // cut short
			}
			var done []progression.Set
			for k := range n {
				rem, err := progression.AdjustInSession(cfg, plan, done)
				if err != nil {
					return User{}, fmt.Errorf("user %d %s: %w", i, cfg.ExerciseID, err)
				}
				tw := rem[0].WeightG
				s := progression.Set{WeightG: tw, Reps: simulateReps(st.e1rmKg, tw, k, rng)}
				done = append(done, s)
				at := started.Add(slot * time.Duration(pos*3+k+1))
				we.Sets = append(we.Sets, Set{Number: k + 1, WeightG: s.WeightG, Reps: s.Reps, LoggedAt: at})
			}
			st.hist = append(st.hist, progression.Session{Date: completed, Plan: plan, Done: done})
			if len(st.hist) > 2 {
				st.hist = st.hist[1:]
			}
			st.sessions++
			// Fast early gains that taper off, with a little noise (so some lifts stall).
			st.e1rmKg *= 1 + 0.012*math.Exp(-float64(st.sessions)/25) + (rng.Float64()-0.5)*0.004
			w.Exercises = append(w.Exercises, we)
		}
		u.Workouts = append(u.Workouts, w)
		last = idx
		u.ProgramUpdatedAt = completed
	}
	u.LastCompletedIndex = last

	// Plate checks: logged on some days, most meals of those days.
	plateRate := 0.45 + 0.4*rng.Float64()
	pProtein, pFiber := 0.5+0.4*rng.Float64(), 0.3+0.5*rng.Float64()
	for d := start; d.Before(p.Anchor); d = d.AddDate(0, 0, 1) {
		if rng.Float64() >= plateRate {
			continue
		}
		for m, meal := range meals {
			if rng.Float64() >= 0.85 {
				continue
			}
			u.Plates = append(u.Plates, PlateCheck{
				Date: d, Meal: meal, Protein: rng.Float64() < pProtein, Fiber: rng.Float64() < pFiber,
				UpdatedAt: localTime(d, 9+5*m, rng.IntN(60), p.Loc),
			})
		}
	}
	return u, nil
}

// startingE1RM returns a beginner-to-intermediate 1RM in kg for the exercise; level is 0–1.
func startingE1RM(cfg progression.Config, level float64, rng *rand.Rand) float64 {
	lo, hi := 35.0, 90.0 // machines and cables, big muscles
	switch {
	case cfg.Equipment == "barbell" && cfg.Size == progression.Big:
		lo, hi = 40, 110
	case cfg.Equipment == "barbell":
		lo, hi = 25, 45
	case cfg.Equipment == "dumbbell" && cfg.Size == progression.Big:
		lo, hi = 14, 35 // per dumbbell
	case cfg.Equipment == "dumbbell" && cfg.Movement == progression.Compound:
		lo, hi = 10, 25
	case cfg.Equipment == "dumbbell":
		lo, hi = 6, 16
	case cfg.Size == progression.Small:
		lo, hi = 15, 45
	}
	return (lo + (hi-lo)*level) * (0.85 + 0.3*rng.Float64())
}

// simulateReps inverts Epley with a little fatigue per set and ±1 noise, clamped to 1–50.
func simulateReps(e1rmKg float64, weightG, setIdx int, rng *rand.Rand) int {
	eff := e1rmKg * (1 - 0.04*float64(setIdx))
	noise := [4]int{-1, 0, 0, 1}[rng.IntN(4)]
	r := int(math.Floor(30*(eff/(float64(weightG)/1000)-1))) + noise
	return min(max(r, progression.MinReps), progression.MaxReps)
}

// localTime is hour:minute local on date (a Date).
func localTime(date time.Time, hour, minute int, loc *time.Location) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc)
}

// uuid returns a version-4 UUID drawn from rng (deterministic, unlike crypto/rand).
func uuid(rng *rand.Rand) [16]byte {
	var b [16]byte
	for i := 0; i < 16; i += 8 {
		v := rng.Uint64()
		for j := range 8 {
			b[i+j] = byte(v >> (8 * j))
		}
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return b
}
