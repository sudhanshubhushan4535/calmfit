-- +goose Up
CREATE TABLE program_templates (
    -- human-readable slug id (e.g. 'ppl'), stable across seeds
    id    text PRIMARY KEY,
    name  text NOT NULL,
    -- array order is the rotation order, e.g. {push,pull,legs}; empty would break the rotation
    days  text[] NOT NULL CHECK (cardinality(days) > 0)
);

-- +goose Down
DROP TABLE program_templates;
