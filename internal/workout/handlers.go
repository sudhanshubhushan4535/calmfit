// Package workout runs a training session: start the next day's workout with the engine's plan,
// log sets, and complete it, returning what changes next time. Every query is scoped to the
// session user; another user's workout is indistinguishable from a missing one (404).
package workout

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
	"github.com/sudhanshubhushan4535/calmfit/internal/program"
	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
)

const (
	statusInProgress = "in_progress"
	statusCompleted  = "completed"

	maxBodyBytes = 4 << 10
	maxWeightG   = 1_000_000 // 1000 kg: far above any real lift, keeps grams well inside int4
)

// Error codes returned as {"error": code}.
const (
	errOnboardingRequired = "onboarding_required"
	errWorkoutNotFound    = "workout_not_found"
	errExerciseNotFound   = "exercise_not_in_workout"
	errWorkoutCompleted   = "workout_completed"
	errNeedsUsualWeight   = "needs_usual_weight"
	errSetsAlreadyLogged  = "sets_already_logged"
	errHasHistory         = "exercise_has_history"
	errNoSetsLogged       = "no_sets_logged"
)

// Handler serves the workout endpoints.
type Handler struct {
	DB  *pgxpool.Pool
	Cfg config.Config    // timezone for local dates and Mon–Sun weeks
	Now func() time.Time // nil means time.Now
}

// Register adds the routes; every route is wrapped in requireUser.
// POSTs without a body (start, complete) rely on the SameSite=Lax session cookie against cross-site posts.
func (h *Handler) Register(mux *http.ServeMux, requireUser func(http.Handler) http.Handler) {
	mux.Handle("GET /workouts/next", requireUser(http.HandlerFunc(h.next)))
	mux.Handle("POST /workouts/start", requireUser(http.HandlerFunc(h.start)))
	mux.Handle("POST /workouts/{id}/exercises/{exerciseID}/cold-start", requireUser(http.HandlerFunc(h.coldStart)))
	mux.Handle("POST /workouts/{id}/exercises/{exerciseID}/later", requireUser(http.HandlerFunc(h.later)))
	mux.Handle("POST /workouts/{id}/sets", requireUser(http.HandlerFunc(h.logSet)))
	mux.Handle("POST /workouts/{id}/complete", requireUser(http.HandlerFunc(h.complete)))
	mux.Handle("GET /progress/weekly", requireUser(http.HandlerFunc(h.weeklyProgress)))
	mux.Handle("GET /progress/{exerciseID}", requireUser(http.HandlerFunc(h.exerciseProgress)))
}

// LoggedSet is one logged working set.
type LoggedSet struct {
	SetNumber int       `json:"set_number"`
	WeightG   int       `json:"weight_g"`
	Reps      int       `json:"reps"`
	LoggedAt  time.Time `json:"logged_at"`
}

// Exercise is one exercise in a workout or a preview.
type Exercise struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TargetMuscle string `json:"target_muscle"`
	MovementType string `json:"movement_type"`
	Equipment    string `json:"equipment"`
	RepScheme    string `json:"rep_scheme"`
	WeightStepG  int    `json:"weight_step_g"`
	RepTargets   []int  `json:"rep_targets"`
	// NeedsUsualWeight is true when there is no history and no cold-start answer yet (Plan is nil).
	NeedsUsualWeight bool              `json:"needs_usual_weight"`
	Plan             *progression.Plan `json:"plan"`
	Sets             []LoggedSet       `json:"sets"`
	// Remaining is the targets for the sets not yet logged (R10); empty once completed.
	Remaining []progression.Target `json:"remaining"`
}

// Workout is a started or completed workout.
type Workout struct {
	ID          string     `json:"id"`
	TemplateID  string     `json:"template_id"`
	Day         string     `json:"day"`
	DayIndex    int        `json:"day_index"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Exercises   []Exercise `json:"exercises"`
}

// Preview is the response of GET /workouts/next.
type Preview struct {
	TemplateID string `json:"template_id"`
	Day        string `json:"day"`
	DayIndex   int    `json:"day_index"`
	// InProgressWorkoutID is set when a workout is already started; POST /workouts/start resumes it.
	InProgressWorkoutID *string    `json:"in_progress_workout_id"`
	Exercises           []Exercise `json:"exercises"`
}

// SessionResult is what was prescribed and what was done for one exercise.
type SessionResult struct {
	Plan progression.Plan  `json:"plan"`
	Done []progression.Set `json:"done"`
}

// SummaryExercise compares this session with the next one for an exercise.
type SummaryExercise struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Skipped     bool              `json:"skipped"`
	ThisSession *SessionResult    `json:"this_session"`
	Next        *progression.Plan `json:"next"` // Next.Summary is the why
}

// Summary is the "next time" screen returned by POST /workouts/{id}/complete.
type Summary struct {
	WorkoutID   string            `json:"workout_id"`
	Day         string            `json:"day"`
	DayIndex    int               `json:"day_index"`
	CompletedAt time.Time         `json:"completed_at"`
	Exercises   []SummaryExercise `json:"exercises"`
}

func newExercise(x exerciseInfo, plan *progression.Plan) (Exercise, error) {
	targets, err := progression.Targets(x.Cfg.Scheme, x.Cfg.RepStep, false)
	if err != nil {
		return Exercise{}, fmt.Errorf("rep targets for %s: %w", x.Cfg.ExerciseID, err)
	}
	return Exercise{
		ID: x.Cfg.ExerciseID, Name: x.Name, TargetMuscle: x.TargetMuscle, MovementType: x.MovementType,
		Equipment: x.Cfg.Equipment, RepScheme: string(x.Cfg.Scheme), WeightStepG: x.Cfg.WeightStepG,
		RepTargets: targets, NeedsUsualWeight: plan == nil, Plan: plan,
		Sets: []LoggedSet{}, Remaining: []progression.Target{},
	}, nil
}

// nextDay reads the user's program and the template exercises of the day after the last completed one.
// Pass lock=true inside a transaction to serialize start/complete per user. ok=false means not onboarded.
func nextDay(ctx context.Context, q querier, uid string, lock bool) (p Preview, xs []exerciseInfo, ok bool, err error) {
	sql := `
		SELECT up.template_id, up.last_completed_index, pt.days
		FROM user_programs up JOIN program_templates pt ON pt.id = up.template_id
		WHERE up.user_id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE OF up`
	}
	var last int
	var days []string
	err = q.QueryRow(ctx, sql, uid).Scan(&p.TemplateID, &last, &days)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, nil, false, nil
	}
	if err != nil {
		return Preview{}, nil, false, fmt.Errorf("load program: %w", err)
	}
	// Rotation is purely sequential; days_per_week doesn't affect which day comes next.
	p.DayIndex = program.NextDayIndex(last, len(days))
	p.Day = days[p.DayIndex]

	rows, err := q.Query(ctx, `
		SELECT `+exerciseCols+`
		FROM template_exercises te JOIN exercises e ON e.id = te.exercise_id
		WHERE te.template_id = $1 AND te.day = $2
		ORDER BY te.position`, p.TemplateID, p.Day)
	if err != nil {
		return Preview{}, nil, false, fmt.Errorf("load template exercises: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		x, err := scanExercise(rows)
		if err != nil {
			return Preview{}, nil, false, fmt.Errorf("scan template exercise: %w", err)
		}
		xs = append(xs, x)
	}
	if err := rows.Err(); err != nil {
		return Preview{}, nil, false, fmt.Errorf("iterate template exercises: %w", err)
	}
	return p, xs, true, nil
}

func (h *Handler) next(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()

	p, xs, ok, err := nextDay(ctx, h.DB, uid, false)
	if err != nil {
		httpx.InternalError(w, "workout: next day", err)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusConflict, errOnboardingRequired)
		return
	}
	var inProgress string
	err = h.DB.QueryRow(ctx, `SELECT id::text FROM workouts WHERE user_id = $1::uuid AND status = 'in_progress'`, uid).Scan(&inProgress)
	switch {
	case err == nil:
		p.InProgressWorkoutID = &inProgress
	case !errors.Is(err, pgx.ErrNoRows):
		httpx.InternalError(w, "workout: in-progress lookup", err)
		return
	}

	p.Exercises = []Exercise{}
	for _, x := range xs {
		plan, err := planFor(ctx, h.DB, uid, x.Cfg)
		if err != nil {
			httpx.InternalError(w, "workout: preview plan", err)
			return
		}
		ex, err := newExercise(x, plan)
		if err != nil {
			httpx.InternalError(w, "workout: preview exercise", err)
			return
		}
		if plan != nil {
			ex.Remaining = plan.Sets
		}
		p.Exercises = append(p.Exercises, ex)
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()

	var id string
	created := false
	err := pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		// Locking the program row serializes concurrent starts (and start vs complete) for this user.
		p, xs, ok, err := nextDay(ctx, tx, uid, true)
		if err != nil {
			return err
		}
		if !ok {
			return errOnboarding
		}
		err = tx.QueryRow(ctx, `SELECT id::text FROM workouts WHERE user_id = $1::uuid AND status = 'in_progress'`, uid).Scan(&id)
		if err == nil {
			return nil // idempotent: resume the open workout
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("in-progress lookup: %w", err)
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO workouts (user_id, template_id, day, day_index, status)
			VALUES ($1::uuid, $2, $3, $4, 'in_progress') RETURNING id::text`,
			uid, p.TemplateID, p.Day, p.DayIndex).Scan(&id); err != nil {
			return fmt.Errorf("insert workout: %w", err)
		}
		for pos, x := range xs {
			plan, err := planFor(ctx, tx, uid, x.Cfg)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO workout_exercises (workout_id, exercise_id, position, plan) VALUES ($1::uuid, $2, $3, $4)`,
				id, x.Cfg.ExerciseID, pos, plan); err != nil {
				return fmt.Errorf("insert workout exercise %s: %w", x.Cfg.ExerciseID, err)
			}
		}
		created = true
		return nil
	})
	if errors.Is(err, errOnboarding) {
		httpx.WriteError(w, http.StatusConflict, errOnboardingRequired)
		return
	}
	if err != nil {
		httpx.InternalError(w, "workout: start", err)
		return
	}

	wo, _, err := loadWorkout(ctx, h.DB, uid, id)
	if err != nil {
		httpx.InternalError(w, "workout: load started workout", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, wo)
}

var errOnboarding = errors.New("workout: onboarding required")

// apiError is a client-facing failure raised inside a transaction.
type apiError struct {
	status int
	code   string
}

func (e apiError) Error() string { return e.code }

// writeTxError maps a transaction error to a response.
func writeTxError(w http.ResponseWriter, op string, err error) {
	var ae apiError
	switch {
	case errors.As(err, &ae):
		httpx.WriteError(w, ae.status, ae.code)
	case errors.Is(err, errNotFound):
		httpx.WriteError(w, http.StatusNotFound, errWorkoutNotFound)
	default:
		httpx.InternalError(w, op, err)
	}
}

// decodeJSON decodes a JSON body capped at maxBodyBytes (see httpx.DecodeJSON).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return httpx.DecodeJSON(w, r, maxBodyBytes, v)
}

// lockOpenExercise locks the user's workout, checks it is still open and contains the exercise,
// and returns the exercise's stored plan (nil before a cold-start answer).
func lockOpenExercise(ctx context.Context, tx pgx.Tx, uid, id, exerciseID string) (*progression.Plan, error) {
	status, _, err := lockWorkout(ctx, tx, uid, id)
	if err != nil {
		return nil, err
	}
	if status != statusInProgress {
		return nil, apiError{http.StatusConflict, errWorkoutCompleted}
	}
	var plan *progression.Plan
	err = tx.QueryRow(ctx, `
		SELECT plan FROM workout_exercises WHERE workout_id = $1::uuid AND exercise_id = $2`,
		id, exerciseID).Scan(&plan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apiError{http.StatusNotFound, errExerciseNotFound}
	}
	if err != nil {
		return nil, fmt.Errorf("load workout exercise: %w", err)
	}
	return plan, nil
}

// writeExercise responds with one exercise's current view (sets and remaining targets).
func (h *Handler) writeExercise(w http.ResponseWriter, ctx context.Context, uid, id, exerciseID, op string) {
	wo, _, err := loadWorkout(ctx, h.DB, uid, id)
	if err != nil {
		httpx.InternalError(w, op, err)
		return
	}
	for _, ex := range wo.Exercises {
		if ex.ID == exerciseID {
			httpx.WriteJSON(w, http.StatusOK, ex)
			return
		}
	}
	httpx.InternalError(w, op, fmt.Errorf("exercise %s vanished from workout %s", exerciseID, id))
}

func (h *Handler) coldStart(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	id, exerciseID := r.PathValue("id"), r.PathValue("exerciseID")

	var body struct {
		UsualKg  *float64 `json:"usual_kg"`
		DontKnow bool     `json:"dont_know"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var cold progression.ColdStart
	switch {
	case body.DontKnow && body.UsualKg == nil:
		cold.DontKnow = true
	case !body.DontKnow && body.UsualKg != nil && *body.UsualKg > 0 && *body.UsualKg*1000 <= maxWeightG:
		cold.UsualWeightG = int(math.Round(*body.UsualKg * 1000))
	default:
		httpx.WriteError(w, http.StatusBadRequest, `send either {"usual_kg": <kg, 0–1000>} or {"dont_know": true}`)
		return
	}

	err := pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		if _, err := lockOpenExercise(ctx, tx, uid, id, exerciseID); err != nil {
			return err
		}
		var logged bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM workout_sets WHERE workout_id = $1::uuid AND exercise_id = $2)`,
			id, exerciseID).Scan(&logged); err != nil {
			return fmt.Errorf("check sets: %w", err)
		}
		if logged {
			return apiError{http.StatusConflict, errSetsAlreadyLogged}
		}
		hist, err := history(ctx, tx, uid, exerciseID, nil)
		if err != nil {
			return err
		}
		if len(hist) > 0 {
			return apiError{http.StatusConflict, errHasHistory}
		}
		x, err := scanExercise(tx.QueryRow(ctx, `SELECT `+exerciseCols+` FROM exercises e WHERE e.id = $1`, exerciseID))
		if err != nil {
			return fmt.Errorf("load exercise: %w", err)
		}
		plan, err := progression.Next(x.Cfg, nil, cold)
		if errors.Is(err, progression.ErrInvalidInput) {
			return apiError{http.StatusBadRequest, err.Error()}
		}
		if err != nil {
			return fmt.Errorf("progression.Next cold start: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE workout_exercises SET plan = $3 WHERE workout_id = $1::uuid AND exercise_id = $2`,
			id, exerciseID, plan)
		return err
	})
	if err != nil {
		writeTxError(w, "workout: cold start", err)
		return
	}
	h.writeExercise(w, ctx, uid, id, exerciseID, "workout: cold start view")
}

// later ("do later") moves an exercise to the end of the user's open workout. Only this workout's
// order changes: the template and every plan are untouched. Moving the last exercise is a no-op,
// so the call is idempotent. The moved row gets max(position)+1; reads only ORDER BY position, so
// gaps are harmless, and moving a single row can't collide with UNIQUE (workout_id, position).
func (h *Handler) later(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	id, exerciseID := r.PathValue("id"), r.PathValue("exerciseID")

	err := pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		if _, err := lockOpenExercise(ctx, tx, uid, id, exerciseID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE workout_exercises we
			SET position = m.max_pos + 1
			FROM (SELECT max(position) AS max_pos FROM workout_exercises WHERE workout_id = $1::uuid) m
			WHERE we.workout_id = $1::uuid AND we.exercise_id = $2 AND we.position < m.max_pos`,
			id, exerciseID)
		if err != nil {
			return fmt.Errorf("move exercise: %w", err)
		}
		return nil
	})
	if err != nil {
		writeTxError(w, "workout: do later", err)
		return
	}
	wo, _, err := loadWorkout(ctx, h.DB, uid, id)
	if err != nil {
		httpx.InternalError(w, "workout: do later view", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, wo)
}

func (h *Handler) logSet(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	id := r.PathValue("id")

	var body struct {
		ExerciseID string `json:"exercise_id"`
		SetNumber  int    `json:"set_number"`
		WeightG    int    `json:"weight_g"`
		Reps       int    `json:"reps"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	switch {
	case body.ExerciseID == "":
		httpx.WriteError(w, http.StatusBadRequest, "exercise_id is required")
		return
	case body.SetNumber < 1 || body.SetNumber > progression.SetsPerExercise:
		httpx.WriteError(w, http.StatusBadRequest, fmt.Sprintf("set_number must be 1–%d", progression.SetsPerExercise))
		return
	case body.WeightG <= 0 || body.WeightG > maxWeightG:
		httpx.WriteError(w, http.StatusBadRequest, "weight_g must be > 0 and at most 1000 kg")
		return
	case body.Reps < progression.MinReps || body.Reps > progression.MaxReps:
		httpx.WriteError(w, http.StatusBadRequest, fmt.Sprintf("reps must be %d–%d", progression.MinReps, progression.MaxReps))
		return
	}

	err := pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		plan, err := lockOpenExercise(ctx, tx, uid, id, body.ExerciseID)
		if err != nil {
			return err
		}
		if plan == nil {
			return apiError{http.StatusConflict, errNeedsUsualWeight}
		}
		// Sets are contiguous from 1, so the done list handed to AdjustInSession is always a prefix.
		if body.SetNumber > 1 {
			var prevLogged bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM workout_sets WHERE workout_id = $1::uuid AND exercise_id = $2 AND set_number = $3)`,
				id, body.ExerciseID, body.SetNumber-1).Scan(&prevLogged); err != nil {
				return fmt.Errorf("check previous set: %w", err)
			}
			if !prevLogged {
				return apiError{http.StatusBadRequest, fmt.Sprintf("log set %d first", body.SetNumber-1)}
			}
		}
		// Upsert: a retry or an edit of the same set never creates a second row.
		_, err = tx.Exec(ctx, `
			INSERT INTO workout_sets (workout_id, exercise_id, set_number, weight_g, reps) VALUES ($1::uuid, $2, $3, $4, $5)
			ON CONFLICT (workout_id, exercise_id, set_number)
			DO UPDATE SET weight_g = EXCLUDED.weight_g, reps = EXCLUDED.reps, logged_at = now()`,
			id, body.ExerciseID, body.SetNumber, body.WeightG, body.Reps)
		return err
	})
	if err != nil {
		writeTxError(w, "workout: log set", err)
		return
	}
	h.writeExercise(w, ctx, uid, id, body.ExerciseID, "workout: log set view")
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	id := r.PathValue("id")

	var sum Summary
	err := pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		// Lock order program row -> workout row, same as start, so the two can't deadlock.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM user_programs WHERE user_id = $1::uuid FOR UPDATE`, uid); err != nil {
			return fmt.Errorf("lock program: %w", err)
		}
		status, dayIndex, err := lockWorkout(ctx, tx, uid, id)
		if err != nil {
			return err
		}
		if status == statusInProgress {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM workout_sets WHERE workout_id = $1::uuid`, id).Scan(&n); err != nil {
				return fmt.Errorf("count sets: %w", err)
			}
			if n == 0 {
				return apiError{http.StatusConflict, errNoSetsLogged}
			}
			if _, err := tx.Exec(ctx, `
				UPDATE workouts SET status = 'completed', completed_at = now() WHERE id = $1::uuid`, id); err != nil {
				return fmt.Errorf("mark completed: %w", err)
			}
			// SET, never increment: replaying a completion can't skip a day.
			if _, err := tx.Exec(ctx, `
				UPDATE user_programs SET last_completed_index = $2, updated_at = now() WHERE user_id = $1::uuid`,
				uid, dayIndex); err != nil {
				return fmt.Errorf("advance program: %w", err)
			}

			// ==== DAY 4 OUTBOX HOOK ====
			// Insert the workout.completed outbox event here, inside this transaction, so it is
			// written if and only if the completion commits. Runs once: only on the in_progress -> completed transition.
			// ===========================
		}
		// Already completed: no writes; the summary is recomputed and is the same (history is bounded by completed_at).
		sum, err = summarize(ctx, tx, uid, id)
		return err
	})
	if err != nil {
		writeTxError(w, "workout: complete", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sum)
}

// summarize builds the "next time" screen: per exercise, this session vs the engine's next plan.
func summarize(ctx context.Context, q querier, uid, id string) (Summary, error) {
	wo, infos, err := loadWorkout(ctx, q, uid, id)
	if err != nil {
		return Summary{}, err
	}
	if wo.CompletedAt == nil {
		return Summary{}, fmt.Errorf("summarize: workout %s is not completed", id)
	}
	sum := Summary{WorkoutID: wo.ID, Day: wo.Day, DayIndex: wo.DayIndex, CompletedAt: *wo.CompletedAt, Exercises: []SummaryExercise{}}
	for _, ex := range wo.Exercises {
		se := SummaryExercise{ID: ex.ID, Name: ex.Name}
		if len(ex.Sets) == 0 || ex.Plan == nil {
			se.Skipped = true
			sum.Exercises = append(sum.Exercises, se)
			continue
		}
		se.ThisSession = &SessionResult{Plan: *ex.Plan, Done: done(ex.Sets)}
		hist, err := history(ctx, q, uid, ex.ID, wo.CompletedAt)
		if err != nil {
			return Summary{}, err
		}
		next, err := progression.Next(infos[ex.ID].Cfg, hist, progression.ColdStart{})
		if err != nil {
			return Summary{}, fmt.Errorf("progression.Next(%s): %w", ex.ID, err)
		}
		se.Next = &next
		sum.Exercises = append(sum.Exercises, se)
	}
	return sum, nil
}
