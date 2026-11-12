package synthetic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
)

// LoadTemplate reads a seeded program template and its exercises' engine configs.
func LoadTemplate(ctx context.Context, db *pgxpool.Pool, id string) (Template, error) {
	t := Template{ID: id, Exercises: map[string][]progression.Config{}}
	if err := db.QueryRow(ctx, `SELECT days FROM program_templates WHERE id = $1`, id).Scan(&t.Days); err != nil {
		return Template{}, fmt.Errorf("template %s: %w (run make seed)", id, err)
	}
	rows, err := db.Query(ctx, `
		SELECT te.day, e.id, coalesce(e.equipment, ''), e.muscle_size, e.movement_type, e.rep_scheme, e.rep_step, e.weight_step_g
		FROM template_exercises te JOIN exercises e ON e.id = te.exercise_id
		WHERE te.template_id = $1 ORDER BY te.day, te.position`, id)
	if err != nil {
		return Template{}, fmt.Errorf("template exercises: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var day, size, mv, scheme string
		var c progression.Config
		if err := rows.Scan(&day, &c.ExerciseID, &c.Equipment, &size, &mv, &scheme, &c.RepStep, &c.WeightStepG); err != nil {
			return Template{}, err
		}
		c.Size, c.Movement, c.Scheme = progression.MuscleSize(size), progression.Movement(mv), progression.RepScheme(scheme)
		t.Exercises[day] = append(t.Exercises[day], c)
	}
	if err := rows.Err(); err != nil {
		return Template{}, err
	}
	for _, d := range t.Days {
		if len(t.Exercises[d]) == 0 {
			return Template{}, fmt.Errorf("template %s: day %s has no exercises", id, d)
		}
	}
	return t, nil
}

// Existing counts generated users already in the DB.
func Existing(ctx context.Context, db *pgxpool.Pool) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM users WHERE google_sub ~ $1`, SubPattern).Scan(&n)
	return n, err
}

// Reset deletes the generated users (cascading to their sessions, program, workouts, sets, plate
// checks and weekly_stats) and their outbox rows. Only subs matching SubPattern are touched.
func Reset(ctx context.Context, db *pgxpool.Pool) (int64, error) {
	var n int64
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE user_id IN (SELECT id FROM users WHERE google_sub ~ $1)`, SubPattern); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM users WHERE google_sub ~ $1`, SubPattern)
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

// Counts are rows per table.
type Counts struct {
	Users, UserPrograms, Workouts, WorkoutExercises, WorkoutSets, PlateChecks int64
}

func (c *Counts) add(u User) {
	c.Users++
	c.UserPrograms++
	c.Workouts += int64(len(u.Workouts))
	for _, w := range u.Workouts {
		c.WorkoutExercises += int64(len(w.Exercises))
		for _, e := range w.Exercises {
			c.WorkoutSets += int64(len(e.Sets))
		}
	}
	c.PlateChecks += int64(len(u.Plates))
}

// On-disk bytes per row including indexes, measured on Postgres 16 after a 4,000-user run
// (pg_total_relation_size / rows, D41). Used only for the budget guard before writing; the real
// size is printed afterwards.
const (
	bytesUser            = 400
	bytesWorkout         = 215
	bytesWorkoutExercise = 765
	bytesWorkoutSet      = 170
	bytesPlateCheck      = 152
)

// EstimatedBytes is the guard's size estimate.
func (c Counts) EstimatedBytes() int64 {
	return c.Users*bytesUser + c.Workouts*bytesWorkout + c.WorkoutExercises*bytesWorkoutExercise +
		c.WorkoutSets*bytesWorkoutSet + c.PlateChecks*bytesPlateCheck
}

// Count generates every user without writing anything and returns the row counts.
func Count(p Params, t Template) (Counts, error) {
	var c Counts
	for i := range p.Users {
		u, err := Generate(p, t, i)
		if err != nil {
			return Counts{}, err
		}
		c.add(u)
	}
	return c, nil
}

// BatchUsers is how many users are generated and copied at a time (bounded memory).
const BatchUsers = 250

// Write generates and COPYs every user in one transaction (all or nothing), in batches of
// BatchUsers, then ANALYZEs the tables. Nothing goes through the outbox (D40).
func Write(ctx context.Context, db *pgxpool.Pool, p Params, t Template, log io.Writer) (Counts, error) {
	var total Counts
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for from := 0; from < p.Users; from += BatchUsers {
			to := min(from+BatchUsers, p.Users)
			users := make([]User, 0, to-from)
			for i := from; i < to; i++ {
				u, err := Generate(p, t, i)
				if err != nil {
					return err
				}
				users = append(users, u)
				total.add(u)
			}
			if err := copyBatch(ctx, tx, t.ID, users); err != nil {
				return err
			}
			fmt.Fprintf(log, "  users %d–%d copied (%d workouts so far)\n", from, to-1, total.Workouts)
		}
		return nil
	})
	if err != nil {
		return Counts{}, err
	}
	if _, err := db.Exec(ctx, `ANALYZE users, user_programs, workouts, workout_exercises, workout_sets, plate_checks`); err != nil {
		return total, fmt.Errorf("analyze: %w", err)
	}
	return total, nil
}

func pgUUID(b [16]byte) pgtype.UUID { return pgtype.UUID{Bytes: b, Valid: true} }

// copyBatch copies the batch table by table in FK order.
func copyBatch(ctx context.Context, tx pgx.Tx, templateID string, users []User) error {
	var us, ups, ws, wes, sets, plates [][]any
	for _, u := range users {
		uid := pgUUID(u.ID)
		us = append(us, []any{uid, u.Sub, u.Email, u.Name, u.CreatedAt})
		ups = append(ups, []any{uid, templateID, u.DaysPerWeek, u.LastCompletedIndex, u.ProgramUpdatedAt})
		for _, w := range u.Workouts {
			wid := pgUUID(w.ID)
			ws = append(ws, []any{wid, uid, templateID, w.Day, w.DayIndex, "completed", w.StartedAt, w.CompletedAt})
			for _, e := range w.Exercises {
				plan, err := json.Marshal(e.Plan)
				if err != nil {
					return err
				}
				wes = append(wes, []any{wid, e.ExerciseID, e.Position, plan})
				for _, s := range e.Sets {
					sets = append(sets, []any{wid, e.ExerciseID, s.Number, s.WeightG, s.Reps, s.LoggedAt})
				}
			}
		}
		for _, pc := range u.Plates {
			plates = append(plates, []any{uid, pc.Date, pc.Meal, pc.Protein, pc.Fiber, pc.UpdatedAt})
		}
	}
	for _, c := range []struct {
		table string
		cols  []string
		rows  [][]any
	}{
		{"users", []string{"id", "google_sub", "email", "name", "created_at"}, us},
		{"user_programs", []string{"user_id", "template_id", "days_per_week", "last_completed_index", "updated_at"}, ups},
		{"workouts", []string{"id", "user_id", "template_id", "day", "day_index", "status", "started_at", "completed_at"}, ws},
		{"workout_exercises", []string{"workout_id", "exercise_id", "position", "plan"}, wes},
		{"workout_sets", []string{"workout_id", "exercise_id", "set_number", "weight_g", "reps", "logged_at"}, sets},
		{"plate_checks", []string{"user_id", "local_date", "meal", "protein", "fiber", "updated_at"}, plates},
	} {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{c.table}, c.cols, pgx.CopyFromRows(c.rows)); err != nil {
			return fmt.Errorf("copy %s: %w", c.table, err)
		}
	}
	return nil
}

// TableSize is one table's row count and total size (heap + indexes + TOAST).
type TableSize struct {
	Table string
	Rows  int64
	Bytes int64
}

// Sizes reports the tables this package writes plus the database size.
func Sizes(ctx context.Context, db *pgxpool.Pool) ([]TableSize, int64, error) {
	var out []TableSize
	for _, t := range []string{"users", "user_programs", "workouts", "workout_exercises", "workout_sets", "plate_checks", "sessions"} {
		ts := TableSize{Table: t}
		if err := db.QueryRow(ctx, `SELECT count(*), pg_total_relation_size($1::regclass) FROM `+pgx.Identifier{t}.Sanitize(), t).
			Scan(&ts.Rows, &ts.Bytes); err != nil {
			return nil, 0, err
		}
		out = append(out, ts)
	}
	var dbBytes int64
	err := db.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&dbBytes)
	return out, dbBytes, err
}

// Elapsed formats a duration for logs.
func Elapsed(start time.Time) string { return time.Since(start).Round(100 * time.Millisecond).String() }
