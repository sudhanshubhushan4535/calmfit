package review

import (
	"strings"
	"testing"
)

func TestBuildWentUpAndStalled(t *testing.T) {
	in := Input{Done: 3, Planned: 3, PlateGoodDays: 5, PlateDays: 7, PlateStreak: 4, Lifts: []Lift{
		{ExerciseID: "a", Name: "Heavier", Before: &Set{20000, 10}, Week: []Set{{22500, 8}, {20000, 12}}},
		{ExerciseID: "b", Name: "More reps", Before: &Set{20000, 10}, Week: []Set{{20000, 11}}},
		{ExerciseID: "c", Name: "Same", Before: &Set{20000, 10}, Week: []Set{{20000, 10}}},
		{ExerciseID: "d", Name: "Lighter", Before: &Set{20000, 10}, Week: []Set{{17500, 12}}},
		{ExerciseID: "e", Name: "First time", Week: []Set{{10000, 10}}},
		{ExerciseID: "f", Name: "Stuck", Before: &Set{30000, 8}, Week: []Set{{30000, 8}}, Stalled: true},
	}}
	r := Build(in)
	if len(r.WentUp) != 2 || r.WentUp[0].ExerciseID != "a" || r.WentUp[0].To != (Set{22500, 8}) ||
		r.WentUp[1].ExerciseID != "b" || r.WentUp[1].From != (Set{20000, 10}) {
		t.Fatalf("went up = %+v", r.WentUp)
	}
	if len(r.Stalled) != 1 || r.Stalled[0].ExerciseID != "f" {
		t.Fatalf("stalled = %+v", r.Stalled)
	}
	if r.Plate != (Plate{GoodDays: 5, Days: 7, Streak: 4}) || r.Done != 3 || r.Planned != 3 {
		t.Fatalf("review = %+v", r)
	}
	if !strings.HasPrefix(r.Tip, "Stuck stalled.") {
		t.Fatalf("tip = %q", r.Tip)
	}
}

func TestTipPriority(t *testing.T) {
	stalled := []Stalled{{"x", "Squat"}}
	cases := []struct {
		name string
		r    Review
		want string
	}{
		{"nothing done", Review{Done: 0, Planned: 3, Stalled: stalled}, "No workouts logged"},
		{"fewer than planned beats stalled", Review{Done: 2, Planned: 3, Stalled: stalled}, "You did 2 of 3"},
		{"stalled beats plate", Review{Done: 3, Planned: 3, Stalled: stalled, Plate: Plate{GoodDays: 0, Days: 7}}, "Squat stalled."},
		{"plate under half", Review{Done: 3, Planned: 3, Plate: Plate{GoodDays: 3, Days: 7}}, "Good plate days: 3 of 7"},
		{"plate exactly half is fine", Review{Done: 4, Planned: 3, Plate: Plate{GoodDays: 2, Days: 4}}, "Solid week"},
		{"all good", Review{Done: 3, Planned: 3, Plate: Plate{GoodDays: 6, Days: 7}}, "Solid week"},
	}
	for _, c := range cases {
		if got := Tip(c.r); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: tip = %q, want prefix %q", c.name, got, c.want)
		}
	}
}

func TestBest(t *testing.T) {
	if b := Best([]Set{{20000, 12}, {22500, 6}, {22500, 8}}); b != (Set{22500, 8}) {
		t.Fatalf("best = %+v", b)
	}
}
