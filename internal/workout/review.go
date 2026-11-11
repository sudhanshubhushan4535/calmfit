package workout

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
	"github.com/sudhanshubhushan4535/calmfit/internal/plate"
	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
	"github.com/sudhanshubhushan4535/calmfit/internal/review"
)

// WeeklyReview is the response of GET /review/weekly.
type WeeklyReview struct {
	WeekStart string `json:"week_start"` // Monday, local
	WeekEnd   string `json:"week_end"`   // Sunday, local
	review.Review
}

func (h *Handler) weeklyReview(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	today := h.today()

	day := today
	if s := r.URL.Query().Get("date"); s != "" {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil || d.After(today) {
			httpx.WriteError(w, http.StatusBadRequest, "date must be YYYY-MM-DD, not in the future")
			return
		}
		day = d
	}
	mon := config.WeekStart(day)
	sun := mon.AddDate(0, 0, 6)

	counts, ok, err := weekCounts(ctx, h.DB, h.Cfg, uid, mon, 1)
	if err != nil {
		httpx.InternalError(w, "review: week counts", err)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusConflict, errOnboardingRequired)
		return
	}
	lifts, err := h.weekLifts(ctx, uid, mon)
	if err != nil {
		httpx.InternalError(w, "review: lifts", err)
		return
	}

	// Plate: only the days of the week that have happened so far.
	last, inProgress := sun, false
	if !today.After(sun) {
		last, inProgress = today, true
	}
	days, err := plate.Days(ctx, h.DB, uid, mon, last, h.Cfg.Plate)
	if err != nil {
		httpx.InternalError(w, "review: plate days", err)
		return
	}
	streak, err := plate.StreakAt(ctx, h.DB, uid, last, inProgress, h.Cfg.Plate)
	if err != nil {
		httpx.InternalError(w, "review: plate streak", err)
		return
	}

	rv := review.Build(review.Input{
		Done: counts[0].Done, Planned: counts[0].Planned, Lifts: lifts,
		PlateGoodDays: plate.GoodDays(days), PlateDays: plate.LoggedDays(days), PlateStreak: streak,
	})
	httpx.WriteJSON(w, http.StatusOK, WeeklyReview{WeekStart: mon.Format(time.DateOnly), WeekEnd: sun.Format(time.DateOnly), Review: rv})
}

// weekLifts loads, per exercise trained in the week starting mon: set 1 of each session that week,
// the last set 1 before it, and whether the engine's plan as of the week's end is stalled.
func (h *Handler) weekLifts(ctx context.Context, uid string, mon time.Time) ([]review.Lift, error) {
	from, to := h.Cfg.StartOf(mon), h.Cfg.StartOf(mon.AddDate(0, 0, 7))
	rows, err := h.DB.Query(ctx, `
		SELECT `+exerciseCols+`, s.weight_g, s.reps
		FROM workouts w
		JOIN workout_sets s ON s.workout_id = w.id AND s.set_number = 1
		JOIN exercises e ON e.id = s.exercise_id
		WHERE w.user_id = $1::uuid AND w.status = 'completed' AND w.completed_at >= $2 AND w.completed_at < $3
		ORDER BY w.completed_at, e.id`, uid, from, to)
	if err != nil {
		return nil, fmt.Errorf("query week sets: %w", err)
	}
	var lifts []review.Lift
	cfgs := map[string]progression.Config{}
	idx := map[string]int{}
	for rows.Next() {
		var s review.Set
		x, err := scanExercise(rows, &s.WeightG, &s.Reps)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan week set: %w", err)
		}
		id := x.Cfg.ExerciseID
		i, seen := idx[id]
		if !seen {
			i = len(lifts)
			idx[id] = i
			cfgs[id] = x.Cfg
			lifts = append(lifts, review.Lift{ExerciseID: id, Name: x.Name})
		}
		lifts[i].Week = append(lifts[i].Week, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate week sets: %w", err)
	}

	// The engine's view as of the end of the week: history up to the last instant of Sunday.
	end := to.Add(-time.Microsecond)
	for i := range lifts {
		l := &lifts[i]
		var before review.Set
		err := h.DB.QueryRow(ctx, `
			SELECT s.weight_g, s.reps FROM workouts w
			JOIN workout_sets s ON s.workout_id = w.id AND s.set_number = 1
			WHERE w.user_id = $1::uuid AND w.status = 'completed' AND w.completed_at < $2 AND s.exercise_id = $3
			ORDER BY w.completed_at DESC, w.started_at DESC LIMIT 1`, uid, from, l.ExerciseID).Scan(&before.WeightG, &before.Reps)
		switch {
		case err == nil:
			l.Before = &before
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("last set before week: %w", err)
		}
		hist, err := history(ctx, h.DB, uid, l.ExerciseID, &end)
		if err != nil {
			return nil, err
		}
		if len(hist) == 0 {
			continue // sets logged without a stored plan can't be judged by the engine
		}
		next, err := progression.Next(cfgs[l.ExerciseID], hist, progression.ColdStart{})
		if err != nil {
			return nil, fmt.Errorf("progression.Next(%s): %w", l.ExerciseID, err)
		}
		l.Stalled = next.Stalled
	}
	return lifts, nil
}
