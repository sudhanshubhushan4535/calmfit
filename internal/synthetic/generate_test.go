package synthetic

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
	"github.com/sudhanshubhushan4535/calmfit/internal/seed"
)

// seedTemplate builds the PPL template from seed/*.json, the same data `make seed` loads.
func seedTemplate(t *testing.T) Template {
	t.Helper()
	var exs []seed.Exercise
	var tpls []seed.Template
	for path, v := range map[string]any{"../../seed/exercises.json": &exs, "../../seed/templates.json": &tpls} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	cat := map[string]seed.Exercise{}
	for _, e := range exs {
		cat[e.ID] = e
	}
	for _, st := range tpls {
		if st.ID != "ppl" {
			continue
		}
		tpl := Template{ID: st.ID, Days: st.Days, Exercises: map[string][]progression.Config{}}
		for _, slot := range st.Exercises {
			e := cat[slot.ExerciseID]
			eq := ""
			if e.Equipment != nil {
				eq = *e.Equipment
			}
			tpl.Exercises[slot.Day] = append(tpl.Exercises[slot.Day], progression.Config{
				ExerciseID: e.ID, Equipment: eq, Size: progression.MuscleSize(e.MuscleSize),
				Movement: progression.Movement(e.MovementType), Scheme: progression.RepScheme(e.RepScheme),
				RepStep: e.RepStep, WeightStepG: e.WeightStepG,
			})
		}
		return tpl
	}
	t.Fatal("no ppl template in seed/templates.json")
	return Template{}
}

func params(t *testing.T, seed uint64) Params {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	return Params{Users: 60, Seed: seed, Anchor: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), Loc: loc}
}

func TestGenerateIsDeterministic(t *testing.T) {
	tpl := seedTemplate(t)
	for i := range 5 {
		a, err := Generate(params(t, 42), tpl, i)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Generate(params(t, 42), tpl, i)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("user %d differs between two runs with the same seed", i)
		}
		c, _ := Generate(params(t, 43), tpl, i)
		if reflect.DeepEqual(a.Workouts, c.Workouts) {
			t.Fatalf("user %d identical with a different seed", i)
		}
	}
}

// Every user's history must be what the API itself could have produced: valid rotation, valid
// set numbering and ranges, times inside the workout, and the engine accepts the history.
func TestGeneratedHistoryIsValid(t *testing.T) {
	tpl := seedTemplate(t)
	p := params(t, 7)
	heavy := 0
	ids := map[[16]byte]bool{}
	for i := range p.Users {
		u, err := Generate(p, tpl, i)
		if err != nil {
			t.Fatal(err)
		}
		if ids[u.ID] {
			t.Fatalf("duplicate user id at %d", i)
		}
		ids[u.ID] = true
		if len(u.Workouts) >= 150 {
			heavy++
		}
		last := -1
		var prevEnd time.Time
		perEx := map[string][]progression.Session{}
		for wi, w := range u.Workouts {
			if w.DayIndex != (last+1)%len(tpl.Days) || tpl.Days[w.DayIndex] != w.Day {
				t.Fatalf("user %d workout %d: day %s/%d breaks the rotation after %d", i, wi, w.Day, w.DayIndex, last)
			}
			last = w.DayIndex
			if !w.CompletedAt.After(w.StartedAt) || w.StartedAt.Before(prevEnd) {
				t.Fatalf("user %d workout %d: bad times", i, wi)
			}
			if !w.CompletedAt.Before(p.Anchor) && w.CompletedAt.In(p.Loc).Format(time.DateOnly) >= p.Anchor.Format(time.DateOnly) {
				t.Fatalf("user %d workout %d: completed on/after the anchor", i, wi)
			}
			prevEnd = w.CompletedAt
			if len(w.Exercises) != len(tpl.Exercises[w.Day]) {
				t.Fatalf("user %d workout %d: %d exercises", i, wi, len(w.Exercises))
			}
			for _, e := range w.Exercises {
				var done []progression.Set
				for k, s := range e.Sets {
					if s.Number != k+1 || s.WeightG <= 0 || s.WeightG > progression.MaxWeightG || s.Reps < 1 || s.Reps > 50 {
						t.Fatalf("user %d %s: bad set %+v", i, e.ExerciseID, s)
					}
					if s.LoggedAt.Before(w.StartedAt) || s.LoggedAt.After(w.CompletedAt) {
						t.Fatalf("user %d %s: set logged outside the workout", i, e.ExerciseID)
					}
					done = append(done, progression.Set{WeightG: s.WeightG, Reps: s.Reps})
				}
				if len(done) > 0 {
					perEx[e.ExerciseID] = append(perEx[e.ExerciseID], progression.Session{Date: w.CompletedAt, Plan: e.Plan, Done: done})
				}
			}
		}
		if u.LastCompletedIndex != last {
			t.Fatalf("user %d: last_completed_index %d, last workout %d", i, u.LastCompletedIndex, last)
		}
		// The API's next workout runs the engine on the last two sessions of each exercise.
		for _, cfgs := range tpl.Exercises {
			for _, cfg := range cfgs {
				h := perEx[cfg.ExerciseID]
				if len(h) > 2 {
					h = h[len(h)-2:]
				}
				if len(h) > 0 {
					if _, err := progression.Next(cfg, h, progression.ColdStart{}); err != nil {
						t.Fatalf("user %d %s: engine rejects the history: %v", i, cfg.ExerciseID, err)
					}
				}
			}
		}
		seen := map[string]bool{}
		for _, pc := range u.Plates {
			k := pc.Date.Format(time.DateOnly) + pc.Meal
			if seen[k] || !pc.Date.Before(p.Anchor) {
				t.Fatalf("user %d: duplicate or future plate check %s", i, k)
			}
			seen[k] = true
		}
	}
	if heavy == 0 {
		t.Error("no heavy users (150+ workouts) in 60: the size mix is off")
	}
}

// The simulated lifters should mostly progress, sometimes stall, and never run away.
func TestGeneratedProgressionIsRealistic(t *testing.T) {
	tpl := seedTemplate(t)
	p := params(t, 42)
	var increases, stalls, sessions int
	for i := range p.Users {
		u, _ := Generate(p, tpl, i)
		for _, w := range u.Workouts {
			for _, e := range w.Exercises {
				sessions++
				if e.Plan.Phase == progression.PhaseBuilding {
					increases++
				}
				if e.Plan.Stalled {
					stalls++
				}
			}
		}
	}
	t.Logf("%d exercise sessions: %d building (after an increase), %d stalled", sessions, increases, stalls)
	if increases == 0 || stalls == 0 || stalls > sessions/4 {
		t.Errorf("unrealistic mix: %d increases, %d stalls of %d", increases, stalls, sessions)
	}
}
