// Package account serves account deletion and the JSON export of a user's data.
// Both act only on the session user; there is no way to name another user.
package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
	"github.com/sudhanshubhushan4535/calmfit/internal/outbox"
)

// Every table in the schema is in exactly one of these lists; a test enforces it, so a new
// table can't be left out of the export (or deletion) silently.
var (
	// ExportedTables hold the user's own data: exported, and emptied on deletion.
	// (users is anonymised into a tombstone rather than deleted.)
	ExportedTables = []string{"users", "user_programs", "workouts", "workout_exercises", "workout_sets",
		"plate_checks", "weekly_stats", "sessions", "push_subscriptions", "reminder_log"}
	// InternalTables are plumbing, not user data. The user's outbox rows are deleted on deletion,
	// except the user.deleted event itself; processed_events holds only event ids.
	InternalTables = []string{"outbox", "processed_events", "goose_db_version"}
	// ReferenceTables are shared seed data with no user columns.
	ReferenceTables = []string{"exercises", "program_templates", "template_exercises"}
)

// Handler serves the account endpoints.
type Handler struct {
	DB  *pgxpool.Pool
	Now func() time.Time // nil means time.Now
}

// Register adds the routes; every route is wrapped in requireUser.
// DELETE can't be sent by a cross-site form, and a cross-site fetch needs a CORS preflight.
func (h *Handler) Register(mux *http.ServeMux, requireUser func(http.Handler) http.Handler) {
	mux.Handle("DELETE /me", requireUser(http.HandlerFunc(h.delete)))
	mux.Handle("GET /me/export", requireUser(http.HandlerFunc(h.export)))
}

var errGone = errors.New("account: user already deleted")

// delete removes the user's data and leaves an anonymised tombstone, in one transaction:
// lock the user (serialises with the worker, which reads it FOR SHARE) → revoke sessions →
// delete owned rows and the user's outbox rows → anonymise → enqueue user.deleted.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	err := pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		var one int
		err := tx.QueryRow(ctx, `SELECT 1 FROM users WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE`, uid).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return errGone // a concurrent delete won the race
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		for _, q := range []string{
			`DELETE FROM sessions WHERE user_id = $1::uuid`,
			`DELETE FROM user_programs WHERE user_id = $1::uuid`,
			`DELETE FROM workouts WHERE user_id = $1::uuid`, // cascades to workout_exercises and workout_sets
			`DELETE FROM plate_checks WHERE user_id = $1::uuid`,
			`DELETE FROM weekly_stats WHERE user_id = $1::uuid`,
			`DELETE FROM push_subscriptions WHERE user_id = $1::uuid`,
			`DELETE FROM reminder_log WHERE user_id = $1::uuid`,
			// Published or not: unpublished events would only be skipped by the worker anyway.
			`DELETE FROM outbox WHERE user_id = $1::uuid`,
			`UPDATE users SET deleted_at = now(), google_sub = NULL, email = '', name = NULL WHERE id = $1::uuid`,
		} {
			if _, err := tx.Exec(ctx, q, uid); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		// After the outbox delete, so this is the user's only remaining event.
		return outbox.Enqueue(ctx, tx, uid, events.TypeUserDeleted, events.V1, events.UserDeleted{})
	})
	if errors.Is(err, errGone) {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		httpx.InternalError(w, "account: delete", err)
		return
	}
	auth.ClearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// Export is the shape of GET /me/export. Sections are built as JSON by Postgres.
type Export struct {
	ExportedAt  time.Time       `json:"exported_at"`
	User        json.RawMessage `json:"user"`
	Program     json.RawMessage `json:"program"`      // null before onboarding
	Workouts    json.RawMessage `json:"workouts"`     // with exercises (plan snapshot) and sets
	PlateChecks json.RawMessage `json:"plate_checks"` // one row per meal
	WeeklyStats json.RawMessage `json:"weekly_stats"` // derived by the worker
	Sessions    json.RawMessage `json:"sessions"`     // times only; the token hash is never exported
	// Push subscriptions without their encryption keys, and the days a reminder was sent.
	PushSubscriptions json.RawMessage `json:"push_subscriptions"`
	Reminders         json.RawMessage `json:"reminders"`
}

// Each query takes the user id as $1 and returns one JSON value.
var exportQueries = []struct {
	name string
	sql  string
}{
	{"user", `SELECT json_build_object('id', id, 'email', email, 'name', name, 'created_at', created_at)
		FROM users WHERE id = $1::uuid`},
	{"program", `SELECT (SELECT json_build_object('template_id', template_id, 'days_per_week', days_per_week,
		'last_completed_index', last_completed_index, 'updated_at', updated_at)
		FROM user_programs WHERE user_id = $1::uuid)`},
	{"workouts", `SELECT coalesce(json_agg(json_build_object(
			'id', w.id, 'template_id', w.template_id, 'day', w.day, 'day_index', w.day_index,
			'status', w.status, 'started_at', w.started_at, 'completed_at', w.completed_at,
			'exercises', (SELECT coalesce(json_agg(json_build_object(
					'exercise_id', we.exercise_id, 'position', we.position, 'plan', we.plan,
					'sets', (SELECT coalesce(json_agg(json_build_object(
							'set_number', s.set_number, 'weight_g', s.weight_g, 'reps', s.reps, 'logged_at', s.logged_at)
						ORDER BY s.set_number), '[]')
						FROM workout_sets s WHERE s.workout_id = we.workout_id AND s.exercise_id = we.exercise_id))
				ORDER BY we.position), '[]')
				FROM workout_exercises we WHERE we.workout_id = w.id))
		ORDER BY w.started_at), '[]')
		FROM workouts w WHERE w.user_id = $1::uuid`},
	{"plate_checks", `SELECT coalesce(json_agg(json_build_object('local_date', local_date, 'meal', meal,
		'protein', protein, 'fiber', fiber, 'updated_at', updated_at) ORDER BY local_date, meal), '[]')
		FROM plate_checks WHERE user_id = $1::uuid`},
	{"weekly_stats", `SELECT coalesce(json_agg(json_build_object('week_start', week_start, 'workouts', workouts,
		'sets', sets, 'volume_g', volume_g, 'updated_at', updated_at) ORDER BY week_start), '[]')
		FROM weekly_stats WHERE user_id = $1::uuid`},
	{"sessions", `SELECT coalesce(json_agg(json_build_object('created_at', created_at, 'expires_at', expires_at)
		ORDER BY created_at), '[]')
		FROM sessions WHERE user_id = $1::uuid`},
	{"push_subscriptions", `SELECT coalesce(json_agg(json_build_object('endpoint', endpoint, 'created_at', created_at)
		ORDER BY created_at), '[]')
		FROM push_subscriptions WHERE user_id = $1::uuid`},
	{"reminders", `SELECT coalesce(json_agg(json_build_object('local_date', local_date, 'sent_at', sent_at)
		ORDER BY local_date), '[]')
		FROM reminder_log WHERE user_id = $1::uuid`},
}

// Load builds the export in one REPEATABLE READ transaction, so all sections are one snapshot.
func Load(ctx context.Context, db *pgxpool.Pool, uid string, now time.Time) (Export, error) {
	x := Export{ExportedAt: now.UTC()}
	dest := map[string]*json.RawMessage{"user": &x.User, "program": &x.Program, "workouts": &x.Workouts,
		"plate_checks": &x.PlateChecks, "weekly_stats": &x.WeeklyStats, "sessions": &x.Sessions,
		"push_subscriptions": &x.PushSubscriptions, "reminders": &x.Reminders}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		for _, q := range exportQueries {
			var b []byte
			if err := tx.QueryRow(ctx, q.sql, uid).Scan(&b); err != nil {
				return fmt.Errorf("export %s: %w", q.name, err)
			}
			if b == nil {
				b = []byte("null")
			}
			*dest[q.name] = b
		}
		return nil
	})
	return x, err
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	x, err := Load(r.Context(), h.DB, uid, now())
	if err != nil {
		httpx.InternalError(w, "account: export", err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="calmfit-export.json"`)
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, x)
}
