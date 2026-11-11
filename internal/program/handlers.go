package program

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
)

const (
	defaultTemplate = "ppl" // the only template in v1
	minDays         = 2
	maxDays         = 6
	maxBodyBytes    = 4 << 10
)

// Handler serves onboarding and next-workout endpoints.
type Handler struct {
	DB *pgxpool.Pool
}

// Register adds the routes; every route is wrapped in requireUser.
func (h *Handler) Register(mux *http.ServeMux, requireUser func(http.Handler) http.Handler) {
	mux.Handle("POST /onboarding", requireUser(http.HandlerFunc(h.onboard)))
	mux.Handle("GET /workouts/next", requireUser(http.HandlerFunc(h.next)))
}

// Program is the user's current program.
type Program struct {
	TemplateID         string    `json:"template_id"`
	DaysPerWeek        int       `json:"days_per_week"`
	LastCompletedIndex int       `json:"last_completed_index"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Exercise is one entry in the next workout.
type Exercise struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TargetMuscle string `json:"target_muscle"`
	MovementType string `json:"movement_type"`
	RepScheme    string `json:"rep_scheme"`
	RepTargets   []int  `json:"rep_targets"`
}

// Workout is the response of GET /workouts/next.
type Workout struct {
	TemplateID string     `json:"template_id"`
	Day        string     `json:"day"`
	DayIndex   int        `json:"day_index"`
	Exercises  []Exercise `json:"exercises"`
}

const badDays = "days_per_week must be an integer between 2 and 6"

func (h *Handler) onboard(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())

	// JSON-only, like /auth/google: cross-site callers would need a CORS preflight.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		httpx.WriteError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	var body struct {
		DaysPerWeek *int `json:"days_per_week"` // pointer so a missing field is distinguishable from 0
	}
	// Decoding into *int rejects 3.5 and "3" as well as malformed JSON.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body); err != nil ||
		body.DaysPerWeek == nil || *body.DaysPerWeek < minDays || *body.DaysPerWeek > maxDays {
		httpx.WriteError(w, http.StatusBadRequest, badDays)
		return
	}

	// Re-onboarding only changes days_per_week: last_completed_index and template_id are
	// deliberately absent from the UPDATE so progress is never reset.
	var p Program
	err := h.DB.QueryRow(r.Context(), `
		INSERT INTO user_programs (user_id, template_id, days_per_week) VALUES ($1::uuid, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET days_per_week = EXCLUDED.days_per_week, updated_at = now()
		RETURNING template_id, days_per_week, last_completed_index, updated_at`,
		uid, defaultTemplate, *body.DaysPerWeek).Scan(&p.TemplateID, &p.DaysPerWeek, &p.LastCompletedIndex, &p.UpdatedAt)
	if err != nil {
		httpx.InternalError(w, "program: upsert user_programs", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) next(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()

	var templateID string
	var last int
	var days []string
	err := h.DB.QueryRow(ctx, `
		SELECT up.template_id, up.last_completed_index, pt.days
		FROM user_programs up JOIN program_templates pt ON pt.id = up.template_id
		WHERE up.user_id = $1::uuid`, uid).Scan(&templateID, &last, &days)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, http.StatusConflict, "onboarding_required")
		return
	}
	if err != nil {
		httpx.InternalError(w, "program: load program", err)
		return
	}

	// Rotation is purely sequential for now; days_per_week doesn't affect which day comes next.
	i := NextDayIndex(last, len(days))
	wo := Workout{TemplateID: templateID, Day: days[i], DayIndex: i, Exercises: []Exercise{}}

	rows, err := h.DB.Query(ctx, `
		SELECT e.id, e.name, e.target_muscle, e.movement_type, e.rep_scheme
		FROM template_exercises te JOIN exercises e ON e.id = te.exercise_id
		WHERE te.template_id = $1 AND te.day = $2
		ORDER BY te.position`, templateID, wo.Day)
	if err != nil {
		httpx.InternalError(w, "program: load exercises", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var e Exercise
		if err := rows.Scan(&e.ID, &e.Name, &e.TargetMuscle, &e.MovementType, &e.RepScheme); err != nil {
			httpx.InternalError(w, "program: scan exercise", err)
			return
		}
		if e.RepTargets, err = RepTargets(e.RepScheme); err != nil {
			httpx.InternalError(w, "program: rep targets", err)
			return
		}
		wo.Exercises = append(wo.Exercises, e)
	}
	if err := rows.Err(); err != nil {
		httpx.InternalError(w, "program: iterate exercises", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, wo)
}
