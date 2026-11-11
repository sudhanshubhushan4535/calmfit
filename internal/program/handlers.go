package program

import (
	"encoding/json"
	"mime"
	"net/http"
	"time"

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

// Handler serves onboarding. The next-workout preview lives in package workout.
type Handler struct {
	DB *pgxpool.Pool
}

// Register adds the routes; every route is wrapped in requireUser.
func (h *Handler) Register(mux *http.ServeMux, requireUser func(http.Handler) http.Handler) {
	mux.Handle("POST /onboarding", requireUser(http.HandlerFunc(h.onboard)))
}

// Program is the user's current program.
type Program struct {
	TemplateID         string    `json:"template_id"`
	DaysPerWeek        int       `json:"days_per_week"`
	LastCompletedIndex int       `json:"last_completed_index"`
	UpdatedAt          time.Time `json:"updated_at"`
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
