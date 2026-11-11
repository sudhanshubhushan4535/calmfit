package workout

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
)

const (
	// maxProgressPoints caps GET /progress/{exerciseID}: the most recent sessions only.
	maxProgressPoints = 50
	defaultWeeks      = 8
	maxWeeks          = 26
)

// ProgressPoint is one completed session's set 1 for an exercise.
type ProgressPoint struct {
	Date    string `json:"date"` // local completion date, YYYY-MM-DD
	WeightG int    `json:"weight_g"`
	Reps    int    `json:"reps"`
}

// ExerciseProgress is the response of GET /progress/{exerciseID}.
type ExerciseProgress struct {
	ExerciseID string          `json:"exercise_id"`
	Name       string          `json:"name"`
	Points     []ProgressPoint `json:"points"` // oldest first
}

// WeekCount is one Mon–Sun local week's completed vs planned workouts.
type WeekCount struct {
	WeekStart string `json:"week_start"` // Monday, YYYY-MM-DD
	Done      int    `json:"done"`
	Planned   int    `json:"planned"`
}

func (h *Handler) today() time.Time {
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	return h.Cfg.Today(now())
}

func (h *Handler) exerciseProgress(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	p := ExerciseProgress{ExerciseID: r.PathValue("exerciseID"), Points: []ProgressPoint{}}

	err := h.DB.QueryRow(ctx, `SELECT name FROM exercises WHERE id = $1`, p.ExerciseID).Scan(&p.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "exercise_not_found")
		return
	}
	if err != nil {
		httpx.InternalError(w, "progress: exercise", err)
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT completed_at, weight_g, reps FROM (
			SELECT w.completed_at, w.started_at, s.weight_g, s.reps
			FROM workouts w
			JOIN workout_sets s ON s.workout_id = w.id
			WHERE w.user_id = $1::uuid AND w.status = 'completed' AND s.exercise_id = $2 AND s.set_number = 1
			ORDER BY w.completed_at DESC, w.started_at DESC
			LIMIT $3
		) recent ORDER BY completed_at, started_at`, uid, p.ExerciseID, maxProgressPoints)
	if err != nil {
		httpx.InternalError(w, "progress: query", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var at time.Time
		var pt ProgressPoint
		if err := rows.Scan(&at, &pt.WeightG, &pt.Reps); err != nil {
			httpx.InternalError(w, "progress: scan", err)
			return
		}
		pt.Date = at.In(h.Cfg.Location).Format(time.DateOnly)
		p.Points = append(p.Points, pt)
	}
	if err := rows.Err(); err != nil {
		httpx.InternalError(w, "progress: rows", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) weeklyProgress(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	weeks := defaultWeeks
	if s := r.URL.Query().Get("weeks"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxWeeks {
			httpx.WriteError(w, http.StatusBadRequest, fmt.Sprintf("weeks must be 1–%d", maxWeeks))
			return
		}
		weeks = n
	}
	last := config.WeekStart(h.today())
	counts, ok, err := weekCounts(r.Context(), h.DB, h.Cfg, uid, last.AddDate(0, 0, -7*(weeks-1)), weeks)
	if err != nil {
		httpx.InternalError(w, "progress: weekly", err)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusConflict, errOnboardingRequired)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"weeks": counts})
}

// weekCounts returns completed vs planned workouts for n Mon–Sun local weeks starting at first
// (a Monday). Planned is the user's current days_per_week: past changes aren't recorded in v1.
// ok=false means the user isn't onboarded.
func weekCounts(ctx context.Context, q querier, cfg config.Config, uid string, first time.Time, n int) ([]WeekCount, bool, error) {
	var planned int
	err := q.QueryRow(ctx, `SELECT days_per_week FROM user_programs WHERE user_id = $1::uuid`, uid).Scan(&planned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load days_per_week: %w", err)
	}
	from, to := cfg.StartOf(first), cfg.StartOf(first.AddDate(0, 0, 7*n))
	rows, err := q.Query(ctx, `
		SELECT completed_at FROM workouts
		WHERE user_id = $1::uuid AND status = 'completed' AND completed_at >= $2 AND completed_at < $3`,
		uid, from, to)
	if err != nil {
		return nil, false, fmt.Errorf("query completed workouts: %w", err)
	}
	defer rows.Close()
	out := make([]WeekCount, n)
	for i := range out {
		out[i] = WeekCount{WeekStart: first.AddDate(0, 0, 7*i).Format(time.DateOnly), Planned: planned}
	}
	for rows.Next() {
		var at time.Time
		if err := rows.Scan(&at); err != nil {
			return nil, false, fmt.Errorf("scan completed workout: %w", err)
		}
		i := int(config.WeekStart(config.Date(at.In(cfg.Location))).Sub(first).Hours()) / (7 * 24)
		if i >= 0 && i < n {
			out[i].Done++
		}
	}
	return out, true, rows.Err()
}
