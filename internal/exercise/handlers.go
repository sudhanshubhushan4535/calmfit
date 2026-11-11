// Package exercise serves an exercise's how-to: instructions, coaching cues and images.
package exercise

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
)

// CuesStatus marks the seeded cues as not yet checked by a coach.
const CuesStatus = "review"

// Handler serves GET /exercises/{id}.
type Handler struct {
	DB *pgxpool.Pool
}

// Register adds the route, wrapped in requireUser.
func (h *Handler) Register(mux *http.ServeMux, requireUser func(http.Handler) http.Handler) {
	mux.Handle("GET /exercises/{id}", requireUser(http.HandlerFunc(h.get)))
}

// HowTo is an exercise's instructions, cues and images. Images are served from /static.
type HowTo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Equipment    *string  `json:"equipment"`
	Instructions []string `json:"instructions"`
	Cues         []string `json:"cues"`
	CuesStatus   string   `json:"cues_status"`
	Images       []string `json:"images"`
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	var x HowTo
	err := h.DB.QueryRow(r.Context(), `
		SELECT id, name, equipment, instructions, cues, image_paths FROM exercises WHERE id = $1`,
		r.PathValue("id")).Scan(&x.ID, &x.Name, &x.Equipment, &x.Instructions, &x.Cues, &x.Images)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "exercise_not_found")
		return
	}
	if err != nil {
		httpx.InternalError(w, "exercise: load", err)
		return
	}
	x.CuesStatus = CuesStatus
	httpx.WriteJSON(w, http.StatusOK, x)
}
