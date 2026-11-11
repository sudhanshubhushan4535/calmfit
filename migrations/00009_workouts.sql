-- +goose Up
CREATE TABLE workouts (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- CASCADE: hard-deleting a user removes their training history
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- RESTRICT: a template with logged workouts can't be deleted. No index: v1 has one template
    template_id   text NOT NULL REFERENCES program_templates (id) ON DELETE RESTRICT,
    -- copied from the template at start so history survives template edits
    day           text NOT NULL,
    -- index into program_templates.days; written to user_programs.last_completed_index on completion
    day_index     int  NOT NULL CHECK (day_index >= 0),
    status        text NOT NULL CHECK (status IN ('in_progress', 'completed')),
    started_at    timestamptz NOT NULL DEFAULT now(),
    completed_at  timestamptz,
    -- completed_at is set exactly when the workout is completed
    CHECK ((status = 'completed') = (completed_at IS NOT NULL))
);

-- At most one in-progress workout per user; also makes POST /workouts/start race-safe
CREATE UNIQUE INDEX workouts_one_in_progress_idx ON workouts (user_id) WHERE status = 'in_progress';
-- Serves the per-exercise history lookup (latest completed workouts) and the user-delete cascade
CREATE INDEX workouts_user_completed_idx ON workouts (user_id, completed_at);

CREATE TABLE workout_exercises (
    workout_id   uuid NOT NULL REFERENCES workouts (id) ON DELETE CASCADE,
    -- RESTRICT: an exercise with logged history can't be deleted
    exercise_id  text NOT NULL REFERENCES exercises (id) ON DELETE RESTRICT,
    -- 0-based order within the workout, copied from the template
    position     int  NOT NULL CHECK (position >= 0),
    -- the engine's Plan snapshot (R17); NULL until a cold-start answer is given
    plan         jsonb,
    PRIMARY KEY (workout_id, exercise_id),
    UNIQUE (workout_id, position)
);

-- Postgres doesn't index FKs automatically; needed for the RESTRICT check and the history join
CREATE INDEX workout_exercises_exercise_id_idx ON workout_exercises (exercise_id);

CREATE TABLE workout_sets (
    workout_id   uuid NOT NULL,
    exercise_id  text NOT NULL,
    set_number   int  NOT NULL CHECK (set_number BETWEEN 1 AND 3),
    -- integer grams, like the engine (R2)
    weight_g     int  NOT NULL CHECK (weight_g > 0),
    reps         int  NOT NULL CHECK (reps BETWEEN 1 AND 50),
    logged_at    timestamptz NOT NULL DEFAULT now(),
    -- one row per set: the upsert target, so retries and edits are idempotent
    PRIMARY KEY (workout_id, exercise_id, set_number),
    -- a set can only belong to an exercise that is in that workout
    FOREIGN KEY (workout_id, exercise_id) REFERENCES workout_exercises (workout_id, exercise_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE workout_sets;
DROP TABLE workout_exercises;
DROP TABLE workouts;
