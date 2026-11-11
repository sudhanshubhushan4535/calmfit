-- +goose Up
-- Nullable first: existing rows are backfilled from equipment before NOT NULL is enforced
ALTER TABLE exercises ADD COLUMN weight_step_g int;

-- Seeded defaults (R2): free weights move 2.5 kg, pin-loaded stacks one 5 kg plate
UPDATE exercises SET weight_step_g = CASE
    WHEN equipment IN ('barbell', 'dumbbell') THEN 2500
    WHEN equipment IN ('machine', 'cable') THEN 5000
END;

ALTER TABLE exercises
    ALTER COLUMN weight_step_g SET NOT NULL,
    -- must be positive or weight progression would stall or go backwards
    ADD CONSTRAINT exercises_weight_step_g_check CHECK (weight_step_g > 0);

-- +goose Down
ALTER TABLE exercises DROP COLUMN weight_step_g;
