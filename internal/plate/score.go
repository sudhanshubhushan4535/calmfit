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
	Date    string           `json:"date"` // YYYY-MM-DD, local
	Meals   map[string]Check `json:"meals"`
	Points  int              `json:"points"`
	Max     int              `json:"max"`
	Percent int              `json:"percent"` // points/max, rounded down
	Good    bool             `json:"good"`
}

// Score scores one day's meals.
func Score(date time.Time, meals map[string]Check, cfg config.Plate) Day {
	d := Day{Date: date.Format(time.DateOnly), Meals: map[string]Check{}, Max: cfg.MaxPoints()}
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

// Streak counts consecutive good days ending at end (a config.Date). If endInProgress is true
// (end is today) and end isn't good yet, the count ends at the day before instead: an unfinished
// today doesn't break a streak.
func Streak(good map[time.Time]bool, end time.Time, endInProgress bool) int {
	d := end
	if endInProgress && !good[d] {
		d = d.AddDate(0, 0, -1)
	}
	n := 0
	for good[d] {
		n++
		d = d.AddDate(0, 0, -1)
	}
	return n
}
