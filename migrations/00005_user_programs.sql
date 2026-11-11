-- +goose Up
CREATE TABLE user_programs (
    -- user_id as PK: one active program per user; CASCADE removes it with the user
    user_id               uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- RESTRICT: a template can't be deleted while users are on it
    template_id           text NOT NULL REFERENCES program_templates (id) ON DELETE RESTRICT,
    -- product range: fewer than 2 or more than 6 days isn't supported
    days_per_week         int  NOT NULL CHECK (days_per_week BETWEEN 2 AND 6),
    -- index into template days; -1 means no workout completed yet, so the next one is index 0
    last_completed_index  int  NOT NULL DEFAULT -1 CHECK (last_completed_index >= -1),
    -- set by the app on each change; no trigger, to keep the logic visible in code
    updated_at            timestamptz NOT NULL DEFAULT now()
);

-- Postgres doesn't index FKs automatically; needed for the RESTRICT check on template deletes
CREATE INDEX user_programs_template_id_idx ON user_programs (template_id);

-- +goose Down
DROP TABLE user_programs;
