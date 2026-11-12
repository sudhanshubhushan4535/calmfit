package drill

import (
	"math/rand/v2"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
)

func TestTotalsAdd(t *testing.T) {
	var tot Totals
	tot.Add([]events.LoggedSet{{WeightG: 20000, Reps: 10}, {WeightG: 22500, Reps: 8}})
	tot.Add([]events.LoggedSet{{WeightG: 5000, Reps: 12}})
	want := Totals{Events: 2, Workouts: 2, Sets: 3, VolumeG: 20000*10 + 22500*8 + 5000*12}
	if tot != want {
		t.Fatalf("got %+v, want %+v", tot, want)
	}
}

func TestRandomSetsAreValidEvents(t *testing.T) {
	sets := RandomSets(rand.New(rand.NewPCG(1, 2)))
	if len(sets) != 15 {
		t.Fatalf("%d sets, want 15", len(sets))
	}
	for _, s := range sets {
		if s.WeightG <= 0 || s.Reps < 1 || s.Reps > 50 || s.SetNumber < 1 || s.SetNumber > 3 {
			t.Fatalf("invalid set %+v", s)
		}
	}
}
