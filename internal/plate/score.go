// Package plate is the daily plate check: for each meal, did the plate have protein and fiber?
// Points per tick and the good-day threshold come from config (config/app.json), not code.
package plate

import (
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
)

// Meals in display order.
var Meals = []string{"breakfast", "lunch", "dinner"}

// ValidMeal reports whether m is one of Meals.
func ValidMeal(m string) bool {
	for _, x := range Meals {
		if x == m {
			return true
		}
	}
	return false
}

// Check is one meal's answers.
type Check struct {
	Protein bool `json:"protein"`
	Fiber   bool `json:"fiber"`
}

// Day is one local date's plate check and score. A meal with no entry scores 0.
type Day struct {
	Date string `json:"date"` // YYYY-MM-DD, local
	// Logged is true when at least one meal has an entry. A day with no entries is neutral for the
	// streak: it neither breaks nor extends it.
	Logged  bool             `json:"logged"`
	Meals   map[string]Check `json:"meals"`
	Points  int              `json:"points"`
	Max     int              `json:"max"`
	Percent int              `json:"percent"` // points/max, rounded down
	Good    bool             `json:"good"`
}

// Score scores one day's meals.
func Score(date time.Time, meals map[string]Check, cfg config.Plate) Day {
	d := Day{Date: date.Format(time.DateOnly), Logged: len(meals) > 0, Meals: map[string]Check{}, Max: cfg.MaxPoints()}
	for _, m := range Meals {
		c := meals[m]
		d.Meals[m] = c
		if c.Protein {
			d.Points += cfg.ProteinPoints
		}
		if c.Fiber {
			d.Points += cfg.FiberPoints
		}
	}
	d.Percent = d.Points * 100 / d.Max
	d.Good = d.Points >= cfg.GoodDayMin
	return d
}

// Streak counts good days back from the last of days (oldest first, consecutive dates):
//   - a good day extends the streak;
//   - a day with no entries is neutral: it's skipped, neither breaking nor extending it;
//   - a logged day under the threshold breaks it;
//   - if lastInProgress (the last day is today), a today that isn't good yet is skipped, since the
//     day isn't over.
func Streak(days []Day, lastInProgress bool) int {
	n := 0
	for i := len(days) - 1; i >= 0; i-- {
		d := days[i]
		switch {
		case d.Good:
			n++
		case !d.Logged, i == len(days)-1 && lastInProgress:
			continue
		default:
			return n
		}
	}
	return n
}

// GoodDays counts good days.
func GoodDays(days []Day) int {
	n := 0
	for _, d := range days {
		if d.Good {
			n++
		}
	}
	return n
}

// LoggedDays counts days with at least one entry.
func LoggedDays(days []Day) int {
	n := 0
	for _, d := range days {
		if d.Logged {
			n++
		}
	}
	return n
}
