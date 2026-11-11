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

// week builds consecutive days from a pattern: G good (6 pts), B logged but not good (2 pts),
// . no entries.
func week(pattern string) []Day {
	var days []Day
	start := day("2026-11-01")
	for i, c := range pattern {
		var meals map[string]Check
		switch c {
		case 'G':
			meals = map[string]Check{"breakfast": both, "lunch": both}
		case 'B':
			meals = map[string]Check{"dinner": protein}
		}
		days = append(days, Score(start.AddDate(0, 0, i), meals, cfg))
	}
	return days
}

func TestStreak(t *testing.T) {
	cases := []struct {
		pattern    string
		inProgress bool
		want       int
	}{
		{"BGGG", false, 3},     // logged bad day ends the count
		{"BGGG", true, 3},      // good today counts
		{"BGG.", true, 2},      // nothing logged today yet
		{"BGGB", true, 2},      // today logged but not good yet: the day isn't over
		{"BGGB", false, 0},     // a finished logged bad day breaks it
		{"G.G..G", false, 3},   // empty days are neutral: they neither break nor extend
		{"G.G..G...", true, 3}, // a run of empty days up to today keeps the streak
		{"GBG.G", false, 2},    // the bad day still breaks it behind the empty day
		{".....", true, 0},     // never logged
		{"", true, 0},
	}
	for _, c := range cases {
		days := week(c.pattern)
		if got := Streak(days, c.inProgress); got != c.want {
			t.Errorf("%q inProgress=%v: streak = %d, want %d", c.pattern, c.inProgress, got, c.want)
		}
	}
	if d := week("."); d[0].Logged {
		t.Errorf("empty day marked logged")
	}
	if d := Score(day("2026-11-10"), map[string]Check{"lunch": {}}, cfg); !d.Logged || d.Points != 0 {
		t.Errorf("an all-unticked entry is logged with 0 points: %+v", d)
	}
}
