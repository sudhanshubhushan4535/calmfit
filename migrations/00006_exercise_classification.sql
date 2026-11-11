-- +goose Up
-- NOT NULL with no default is safe: exercises is still empty before the first seed
ALTER TABLE exercises
    -- our fine-grained tag (e.g. side_delt); dataset primaryMuscles is too coarse ("shoulders")
    ADD COLUMN target_muscle text NOT NULL,
    -- CHECK like muscle_size/rep_scheme: validation rules depend on exactly these two values
    ADD COLUMN movement_type text NOT NULL CHECK (movement_type IN ('compound', 'isolation'));

-- +goose Down
ALTER TABLE exercises
    DROP COLUMN movement_type,
    DROP COLUMN target_muscle;
