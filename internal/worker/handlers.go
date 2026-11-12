package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/plate"
)

// Key selects a handler: an event type at one payload version.
type Key struct {
	Type    string
	Version int
}

// HandlerFunc applies one event inside the worker's transaction. Return Permanent(err) when the
// event can never succeed (bad payload); any other error is treated as transient.
type HandlerFunc func(ctx context.Context, tx pgx.Tx, e events.Envelope) error

// PermanentError marks an event that will fail on every attempt, so retrying it is pointless.
type PermanentError struct{ Err error }

func (e PermanentError) Error() string { return "permanent: " + e.Err.Error() }
func (e PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as a PermanentError.
func Permanent(err error) error { return PermanentError{err} }

// IsPermanent reports whether err (or anything it wraps) is a PermanentError.
func IsPermanent(err error) bool {
	var p PermanentError
	return errors.As(err, &p)
}

// DefaultHandlers are the production handlers for every event type this code writes.
func DefaultHandlers(cfg config.Config) map[Key]HandlerFunc {
	return map[Key]HandlerFunc{
		{events.TypeWorkoutLogged, events.V1}: workoutLogged(cfg),
		{events.TypePlateChecked, events.V1}:  plateChecked,
		{events.TypeUserDeleted, events.V1}:   userDeleted,
	}
}

func decodeData[T any](e events.Envelope) (T, error) {
	var v T
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return v, Permanent(fmt.Errorf("%s data: %w", e.Type, err))
	}
	return v, nil
}

// workoutLogged adds the workout to its week's stats. It increments, so it is only correct
// because processed_events guarantees each event is applied once.
func workoutLogged(cfg config.Config) HandlerFunc {
	return func(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
		ev, err := decodeData[events.WorkoutLogged](e)
		if err != nil {
			return err
		}
		if ev.WorkoutID == "" || ev.CompletedAt.IsZero() || len(ev.Sets) == 0 {
			return Permanent(fmt.Errorf("workout.logged: missing workout_id, completed_at or sets"))
		}
		var volume int64
		for _, s := range ev.Sets {
			if s.WeightG <= 0 || s.Reps <= 0 {
				return Permanent(fmt.Errorf("workout.logged: invalid set %+v", s))
			}
			volume += int64(s.WeightG) * int64(s.Reps)
		}
		week := config.WeekStart(cfg.Today(ev.CompletedAt))
		if _, err := tx.Exec(ctx, `
			INSERT INTO weekly_stats (user_id, week_start, workouts, sets, volume_g) VALUES ($1::uuid, $2, 1, $3, $4)
			ON CONFLICT (user_id, week_start) DO UPDATE SET
				workouts = weekly_stats.workouts + 1,
				sets = weekly_stats.sets + EXCLUDED.sets,
				volume_g = weekly_stats.volume_g + EXCLUDED.volume_g,
				updated_at = now()`,
			e.UserID, week, len(ev.Sets), volume); err != nil {
			return fmt.Errorf("weekly_stats upsert: %w", err)
		}
		return nil
	}
}

// plateChecked validates the event and records it as processed. v1 has no plate projection;
// the event exists for analytics (Day 7).
func plateChecked(_ context.Context, _ pgx.Tx, e events.Envelope) error {
	ev, err := decodeData[events.PlateChecked](e)
	if err != nil {
		return err
	}
	if _, err := time.Parse(time.DateOnly, ev.LocalDate); err != nil || !plate.ValidMeal(ev.Meal) {
		return Permanent(fmt.Errorf("plate.checked: invalid date %q or meal %q", ev.LocalDate, ev.Meal))
	}
	return nil
}

// userDeleted removes the user's derived rows. The deletion transaction already does this; doing
// it again here is idempotent and covers rows written by an older code path.
func userDeleted(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
	if _, err := tx.Exec(ctx, `DELETE FROM weekly_stats WHERE user_id = $1::uuid`, e.UserID); err != nil {
		return fmt.Errorf("purge weekly_stats: %w", err)
	}
	return nil
}
