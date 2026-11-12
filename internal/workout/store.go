package workout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
)

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so loaders work in and out of a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var errNotFound = errors.New("workout: not found")

// exerciseInfo is an exercise row: display fields plus its engine config.
type exerciseInfo struct {
	Name         string
	TargetMuscle string
	MovementType string
	Cfg          progression.Config
}

// exerciseCols must be scanned with scanExercise; the table alias is e.
const exerciseCols = `e.id, e.name, e.target_muscle, e.movement_type, coalesce(e.equipment, ''),
	e.muscle_size, e.rep_scheme, e.rep_step, e.weight_step_g`

func scanExercise(row pgx.Row, extra ...any) (exerciseInfo, error) {
	var x exerciseInfo
	var size, scheme string
	dest := append([]any{&x.Cfg.ExerciseID, &x.Name, &x.TargetMuscle, &x.MovementType, &x.Cfg.Equipment,
		&size, &scheme, &x.Cfg.RepStep, &x.Cfg.WeightStepG}, extra...)
	if err := row.Scan(dest...); err != nil {
		return exerciseInfo{}, err
	}
	x.Cfg.Size = progression.MuscleSize(size)
	x.Cfg.Movement = progression.Movement(x.MovementType)
	x.Cfg.Scheme = progression.RepScheme(scheme)
	return x, nil
}

// history returns the user's last two completed sessions of an exercise, oldest first (R17).
// A session is a completed workout with at least one logged set on the exercise. If upTo is set,
// only workouts completed at or before it count, so a workout's summary never changes later.
func history(ctx context.Context, q querier, uid, exerciseID string, upTo *time.Time) ([]progression.Session, error) {
	rows, err := q.Query(ctx, `
		SELECT w.completed_at, we.plan,
		       array_agg(s.weight_g ORDER BY s.set_number), array_agg(s.reps ORDER BY s.set_number)
		FROM workouts w
		JOIN workout_exercises we ON we.workout_id = w.id
		JOIN workout_sets s ON s.workout_id = we.workout_id AND s.exercise_id = we.exercise_id
		WHERE w.user_id = $1::uuid AND w.status = 'completed' AND we.exercise_id = $2
		  AND we.plan IS NOT NULL
		  AND ($3::timestamptz IS NULL OR w.completed_at <= $3)
		GROUP BY w.id, w.completed_at, w.started_at, we.plan
		ORDER BY w.completed_at DESC, w.started_at DESC
		LIMIT 2`, uid, exerciseID, upTo)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	var out []progression.Session
	for rows.Next() {
		var s progression.Session
		var weights, reps []int
		if err := rows.Scan(&s.Date, &s.Plan, &weights, &reps); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan history: %w", err)
		}
		for i := range weights {
			s.Done = append(s.Done, progression.Set{WeightG: weights[i], Reps: reps[i]})
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history: %w", err)
	}
	// Newest first from SQL; the engine wants date order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// planFor returns the engine's plan from history, or nil when there is none and a cold-start answer is needed.
func planFor(ctx context.Context, q querier, uid string, cfg progression.Config) (*progression.Plan, error) {
	hist, err := history(ctx, q, uid, cfg.ExerciseID, nil)
	if err != nil {
		return nil, err
	}
	if len(hist) == 0 {
		return nil, nil
	}
	p, err := progression.Next(cfg, hist, progression.ColdStart{})
	if err != nil {
		return nil, fmt.Errorf("progression.Next(%s): %w", cfg.ExerciseID, err)
	}
	return &p, nil
}

// validUUID reports whether s parses as a UUID, so a malformed path id is a 404, not a cast error.
func validUUID(s string) bool {
	var u pgtype.UUID
	return u.Scan(s) == nil
}

// lockWorkout locks the user's workout row and returns its status and day index; errNotFound if it
// doesn't exist or belongs to someone else.
func lockWorkout(ctx context.Context, tx pgx.Tx, uid, id string) (status string, dayIndex int, err error) {
	if !validUUID(id) {
		return "", 0, errNotFound
	}
	err = tx.QueryRow(ctx, `
		SELECT status, day_index FROM workouts WHERE id = $1::uuid AND user_id = $2::uuid FOR UPDATE`,
		id, uid).Scan(&status, &dayIndex)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, errNotFound
	}
	return status, dayIndex, err
}

// loadWorkout returns the full view of the user's workout; errNotFound if it isn't theirs.
func loadWorkout(ctx context.Context, q querier, uid, id string) (Workout, map[string]exerciseInfo, error) {
	if !validUUID(id) {
		return Workout{}, nil, errNotFound
	}
	var w Workout
	err := q.QueryRow(ctx, `
		SELECT id::text, template_id, day, day_index, status, started_at, completed_at
		FROM workouts WHERE id = $1::uuid AND user_id = $2::uuid`, id, uid).
		Scan(&w.ID, &w.TemplateID, &w.Day, &w.DayIndex, &w.Status, &w.StartedAt, &w.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workout{}, nil, errNotFound
	}
	if err != nil {
		return Workout{}, nil, fmt.Errorf("load workout: %w", err)
	}

	rows, err := q.Query(ctx, `
		SELECT `+exerciseCols+`, we.plan
		FROM workout_exercises we JOIN exercises e ON e.id = we.exercise_id
		WHERE we.workout_id = $1::uuid
		ORDER BY we.position`, id)
	if err != nil {
		return Workout{}, nil, fmt.Errorf("load workout exercises: %w", err)
	}
	infos := map[string]exerciseInfo{}
	byID := map[string]int{}
	for rows.Next() {
		var plan *progression.Plan
		x, err := scanExercise(rows, &plan)
		if err != nil {
			rows.Close()
			return Workout{}, nil, fmt.Errorf("scan workout exercise: %w", err)
		}
		ex, err := newExercise(x, plan)
		if err != nil {
			rows.Close()
			return Workout{}, nil, err
		}
		infos[x.Cfg.ExerciseID] = x
		byID[x.Cfg.ExerciseID] = len(w.Exercises)
		w.Exercises = append(w.Exercises, ex)
	}
	if err := rows.Err(); err != nil {
		return Workout{}, nil, fmt.Errorf("iterate workout exercises: %w", err)
	}

	rows, err = q.Query(ctx, `
		SELECT exercise_id, set_number, weight_g, reps, logged_at
		FROM workout_sets WHERE workout_id = $1::uuid
		ORDER BY exercise_id, set_number`, id)
	if err != nil {
		return Workout{}, nil, fmt.Errorf("load sets: %w", err)
	}
	for rows.Next() {
		var exID string
		var s LoggedSet
		if err := rows.Scan(&exID, &s.SetNumber, &s.WeightG, &s.Reps, &s.LoggedAt); err != nil {
			rows.Close()
			return Workout{}, nil, fmt.Errorf("scan set: %w", err)
		}
		ex := &w.Exercises[byID[exID]]
		ex.Sets = append(ex.Sets, s)
	}
	if err := rows.Err(); err != nil {
		return Workout{}, nil, fmt.Errorf("iterate sets: %w", err)
	}

	// Remaining targets only make sense while the workout is open.
	for i := range w.Exercises {
		ex := &w.Exercises[i]
		ex.Remaining = []progression.Target{}
		if ex.Plan == nil || w.Status != statusInProgress {
			continue
		}
		if ex.Remaining, err = progression.AdjustInSession(infos[ex.ID].Cfg, *ex.Plan, done(ex.Sets)); err != nil {
			return Workout{}, nil, fmt.Errorf("adjust %s: %w", ex.ID, err)
		}
	}
	return w, infos, nil
}

// done converts logged sets (contiguous from set 1) to engine sets.
func done(sets []LoggedSet) []progression.Set {
	out := make([]progression.Set, len(sets))
	for i, s := range sets {
		out[i] = progression.Set{WeightG: s.WeightG, Reps: s.Reps}
	}
	return out
}

// loggedSets returns a workout's sets for the workout.logged event, in workout order.
func loggedSets(ctx context.Context, q querier, workoutID string) ([]events.LoggedSet, error) {
	rows, err := q.Query(ctx, `
		SELECT s.exercise_id, s.set_number, s.weight_g, s.reps
		FROM workout_sets s
		JOIN workout_exercises we ON we.workout_id = s.workout_id AND we.exercise_id = s.exercise_id
		WHERE s.workout_id = $1::uuid
		ORDER BY we.position, s.set_number`, workoutID)
	if err != nil {
		return nil, fmt.Errorf("load sets for event: %w", err)
	}
	sets, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (events.LoggedSet, error) {
		var s events.LoggedSet
		err := r.Scan(&s.ExerciseID, &s.SetNumber, &s.WeightG, &s.Reps)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan sets for event: %w", err)
	}
	return sets, nil
}
