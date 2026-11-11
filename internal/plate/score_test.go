package plate

import (
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
)

var cfg = config.Plate{ProteinPoints: 2, FiberPoints: 1, GoodDayMin: 6}

var (
	both    = Check{Protein: true, Fiber: true}
	protein = Check{Protein: true}
	fiber   = Check{Fiber: true}
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

// The five example days from docs/BUILD_SPEC.md ("Plate check and protein guide").
func TestSpecExampleDays(t *testing.T) {
	cases := []struct {
		name    string
		meals   map[string]Check
		points  int
		percent int
		good    bool
	}{
		{"Protein in all 3 meals, no fiber",
			map[string]Check{"breakfast": protein, "lunch": protein, "dinner": protein}, 6, 66, true},
		{"2 meals with both, 1 meal nothing",
			map[string]Check{"breakfast": both, "lunch": both}, 6, 66, true},
		{"Protein in all 3, fiber in 1",
			map[string]Check{"breakfast": both, "lunch": protein, "dinner": protein}, 7, 77, true},
		{"Fiber in all 3, protein in 1",
			map[string]Check{"breakfast": both, "lunch": fiber, "dinner": fiber}, 5, 55, false},
		{"Protein in 2 meals only",
			map[string]Check{"breakfast": protein, "lunch": protein}, 4, 44, false},
	}
	for _, c := range cases {
		d := Score(day("2026-11-10"), c.meals, cfg)
		if d.Points != c.points || d.Percent != c.percent || d.Good != c.good || d.Max != 9 {
			t.Errorf("%s: got points=%d percent=%d good=%v max=%d, want %d/%d/%v/9",
				c.name, d.Points, d.Percent, d.Good, d.Max, c.points, c.percent, c.good)
		}
	}
}

func TestScoreEmptyAndPerfect(t *testing.T) {
	if d := Score(day("2026-11-10"), nil, cfg); d.Points != 0 || d.Good || len(d.Meals) != 3 {
		t.Fatalf("empty day: %+v", d)
	}
	full := map[string]Check{"breakfast": both, "lunch": both, "dinner": both}
	if d := Score(day("2026-11-10"), full, cfg); d.Points != 9 || d.Percent != 100 || !d.Good {
		t.Fatalf("perfect day: %+v", d)
	}
}

func TestScoreUsesConfigWeights(t *testing.T) {
	alt := config.Plate{ProteinPoints: 1, FiberPoints: 1, GoodDayMin: 4}
	d := Score(day("2026-11-10"), map[string]Check{"breakfast": both, "lunch": both}, alt)
	if d.Points != 4 || d.Max != 6 || !d.Good {
		t.Fatalf("alt weights: %+v", d)
	}
}

func TestStreak(t *testing.T) {
	good := map[time.Time]bool{
		day("2026-11-06"): true,
		day("2026-11-08"): true, day("2026-11-09"): true, day("2026-11-10"): true,
	}
	cases := []struct {
		name       string
		end        string
		inProgress bool
		want       int
	}{
		{"today good", "2026-11-10", true, 3},
		{"today not logged yet", "2026-11-11", true, 3},
		{"past day not good breaks", "2026-11-11", false, 0},
		{"gap stops count", "2026-11-08", false, 1},
		{"two missed days", "2026-11-12", true, 0},
	}
	for _, c := range cases {
		if got := Streak(good, day(c.end), c.inProgress); got != c.want {
			t.Errorf("%s: streak = %d, want %d", c.name, got, c.want)
		}
	}
}
