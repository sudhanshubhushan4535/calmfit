// Package analytics records the six product events from the build spec in our own Postgres table
// (D48). There is no third-party tracker. docs/PILOT_METRICS.sql turns the events into the pilot
// metrics.
package analytics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
)

// The spec's events. The table's CHECK constraint allows exactly these.
const (
	SignupCompleted        = "signup_completed"
	FirstWorkoutCompleted  = "first_workout_completed"
	SecondWorkoutCompleted = "second_workout_completed"
	WorkoutCompleted       = "workout_completed"
	NextTargetViewed       = "next_target_viewed"
	PlateChecked           = "plate_checked"
)

// Execer is a pool or a transaction. Server-side events are recorded in the transaction of the
// write they describe, so an event exists if and only if that write committed.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Record stores one event for the user, timestamped by the database.
func Record(ctx context.Context, q Execer, uid, name string) error {
	if _, err := q.Exec(ctx, `INSERT INTO analytics_events (user_id, name) VALUES ($1::uuid, $2)`, uid, name); err != nil {
		return fmt.Errorf("analytics %s: %w", name, err)
	}
	return nil
}

// WorkoutEvents returns the events for a user's n-th completed workout (n counts this one).
func WorkoutEvents(n int) []string {
	switch n {
	case 1:
		return []string{FirstWorkoutCompleted, WorkoutCompleted}
	case 2:
		return []string{SecondWorkoutCompleted, WorkoutCompleted}
	default:
		return []string{WorkoutCompleted}
	}
}

// clientEvents are the only events the app may post; every other event is recorded by the server
// where it happens, so a client can't fake signups or workouts.
var clientEvents = map[string]bool{NextTargetViewed: true}

// Handler serves POST /events.
type Handler struct {
	DB     *pgxpool.Pool
	UserID func(context.Context) (string, bool)
}

// Register adds POST /events behind requireUser.
func (h *Handler) Register(mux *http.ServeMux, requireUser func(http.Handler) http.Handler) {
	mux.Handle("POST /events", requireUser(http.HandlerFunc(h.post)))
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) {
	uid, _ := h.UserID(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(w, r, 1<<10, &body) {
		return
	}
	if !clientEvents[body.Name] {
		httpx.WriteError(w, http.StatusBadRequest, "name must be next_target_viewed")
		return
	}
	if err := Record(r.Context(), h.DB, uid, body.Name); err != nil {
		httpx.InternalError(w, "analytics: record", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
