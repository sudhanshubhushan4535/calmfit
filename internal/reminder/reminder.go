// Package reminder sends workout-day Web Push reminders (D47). Users subscribe from the app after
// a completed workout; a cron job runs cmd/reminders once a day. Who is due and what the message
// says are pure functions here. The message comes from workout.Next, so it always matches the app.
package reminder

import (
	"fmt"
	"strings"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

// Gap is the fewest days between a completed workout and the next reminder: 7 / days_per_week,
// rounded down, at least 1. 2 days a week → every 3 days, 3 → every 2, 4–6 → daily.
func Gap(daysPerWeek int) int {
	if daysPerWeek < 1 {
		return 7
	}
	return max(1, 7/daysPerWeek)
}

// Due reports whether a user gets a reminder today. today and lastDone are local dates (midnight
// in the app's zone). lastDone nil = no completed workout yet, so due. A workout started today means
// they're already training, so not due.
func Due(today time.Time, lastDone *time.Time, daysPerWeek int, startedToday bool) bool {
	if startedToday {
		return false
	}
	if lastDone == nil {
		return true
	}
	days := int(today.Sub(*lastDone).Hours()/24 + 0.5) // DST-safe rounding of whole local days
	return days >= Gap(daysPerWeek)
}

// Text is the notification body, e.g. "Pull day today. Beat 45 kg on the lat pulldown.": the
// next workout's day and the engine's set-1 target for its first exercise that has a plan.
// Without any plan yet (no history) it only names the day.
func Text(p workout.Preview) string {
	day := p.Day
	if day != "" {
		day = strings.ToUpper(day[:1]) + day[1:]
	}
	for _, ex := range p.Exercises {
		if ex.Plan != nil && len(ex.Plan.Sets) > 0 {
			return fmt.Sprintf("%s day today. Beat %s kg on the %s.", day, kg(ex.Plan.Sets[0].WeightG), shortName(ex.Name))
		}
	}
	return day + " day today. Your plan is ready."
}

// shortName drops a parenthesised detail and lowercases: "Lat Pulldown (shoulder-width grip)" →
// "lat pulldown".
func shortName(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

func kg(g int) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", float64(g)/1000), "0"), ".")
}
