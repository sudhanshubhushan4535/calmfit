-- +goose Up
CREATE TABLE template_exercises (
    -- CASCADE: deleting a template removes its rows
    template_id  text NOT NULL REFERENCES program_templates (id) ON DELETE CASCADE,
    -- must be one of program_templates.days; checked in seed/app code because Postgres can't FK into an array element
    day          text NOT NULL,
    -- 0-based order within the day
    position     int  NOT NULL CHECK (position >= 0),
    -- RESTRICT: an exercise can't be deleted while a template uses it
    exercise_id  text NOT NULL REFERENCES exercises (id) ON DELETE RESTRICT,
    -- one exercise per slot; this index also serves "load template ordered by day, position"
    PRIMARY KEY (template_id, day, position)
);

-- Postgres doesn't index FKs automatically; needed for the RESTRICT check on exercise deletes
CREATE INDEX template_exercises_exercise_id_idx ON template_exercises (exercise_id);

-- +goose Down
DROP TABLE template_exercises;
