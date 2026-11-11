// Command seed validates seed/*.json and upserts exercises and templates in one transaction.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/sudhanshubhushan4535/calmfit/internal/seed"
)

const (
	exercisesFile = "seed/exercises.json"
	templatesFile = "seed/templates.json"
)

func main() {
	var exercises []seed.Exercise
	var templates []seed.Template
	mustLoad(exercisesFile, &exercises)
	mustLoad(templatesFile, &templates)

	catalog := make(map[string]seed.Exercise, len(exercises))
	for _, e := range exercises {
		if _, dup := catalog[e.ID]; dup {
			log.Fatalf("validation failed: duplicate exercise id %s in %s", e.ID, exercisesFile)
		}
		catalog[e.ID] = e
	}

	// Validate everything before touching the DB.
	var problems []string
	for _, e := range exercises {
		if err := seed.ValidateExercise(e); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for _, t := range templates {
		if err := seed.Validate(t, catalog); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "validation failed:\n  %s\n", strings.ReplaceAll(strings.Join(problems, "\n"), "\n", "\n  "))
		os.Exit(1)
	}
	for _, t := range templates {
		if missing := seed.Coverage(t, catalog); len(missing) > 0 {
			fmt.Printf("warning: template %s has no exercise for: %s\n", t.ID, strings.Join(missing, ", "))
		}
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set (copy .env.example to .env)")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	slots := 0
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		for _, e := range exercises {
			if err := upsertExercise(ctx, tx, e); err != nil {
				return fmt.Errorf("exercise %s: %w", e.ID, err)
			}
		}
		for _, t := range templates {
			n, err := upsertTemplate(ctx, tx, t)
			if err != nil {
				return fmt.Errorf("template %s: %w", t.ID, err)
			}
			slots += n
		}
		return nil
	})
	if err != nil {
		log.Fatalf("seed (rolled back): %v", err)
	}
	fmt.Printf("seeded %d exercises, %d templates, %d template slots\n", len(exercises), len(templates), slots)
}

func mustLoad(path string, v any) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields() // typos in seed files should fail loudly
	if err := dec.Decode(v); err != nil {
		log.Fatalf("parse %s: %v", path, err)
	}
}

func upsertExercise(ctx context.Context, tx pgx.Tx, e seed.Exercise) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO exercises (id, name, aliases, equipment, primary_muscles, target_muscle, movement_type,
		                       muscle_size, rep_scheme, rep_step, weight_step_g, image_paths, instructions, cues)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name, aliases = EXCLUDED.aliases, equipment = EXCLUDED.equipment,
			primary_muscles = EXCLUDED.primary_muscles, target_muscle = EXCLUDED.target_muscle,
			movement_type = EXCLUDED.movement_type, muscle_size = EXCLUDED.muscle_size,
			rep_scheme = EXCLUDED.rep_scheme, rep_step = EXCLUDED.rep_step, weight_step_g = EXCLUDED.weight_step_g,
			image_paths = EXCLUDED.image_paths, instructions = EXCLUDED.instructions, cues = EXCLUDED.cues`,
		e.ID, e.Name, nonNil(e.Aliases), e.Equipment, nonNil(e.PrimaryMuscles), e.TargetMuscle, e.MovementType,
		e.MuscleSize, e.RepScheme, e.RepStep, e.WeightStepG, nonNil(e.ImagePaths), nonNil(e.Instructions), nonNil(e.Cues))
	return err
}

// upsertTemplate writes the template and its slots, removes stale slots, and returns the slot count.
func upsertTemplate(ctx context.Context, tx pgx.Tx, t seed.Template) (int, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO program_templates (id, name, days) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, days = EXCLUDED.days`,
		t.ID, t.Name, t.Days); err != nil {
		return 0, err
	}

	pos := map[string]int{}
	var keepDays []string
	var keepPos []int32
	for _, te := range t.Exercises {
		p := pos[te.Day]
		pos[te.Day]++
		if _, err := tx.Exec(ctx, `
			INSERT INTO template_exercises (template_id, day, position, exercise_id) VALUES ($1, $2, $3, $4)
			ON CONFLICT (template_id, day, position) DO UPDATE SET exercise_id = EXCLUDED.exercise_id`,
			t.ID, te.Day, p, te.ExerciseID); err != nil {
			return 0, fmt.Errorf("slot %s/%d: %w", te.Day, p, err)
		}
		keepDays = append(keepDays, te.Day)
		keepPos = append(keepPos, int32(p))
	}

	// Drop slots no longer in the JSON so a shortened routine leaves nothing stale.
	if _, err := tx.Exec(ctx, `
		DELETE FROM template_exercises te
		WHERE te.template_id = $1
		  AND (te.day, te.position) NOT IN (SELECT * FROM unnest($2::text[], $3::int[]))`,
		t.ID, keepDays, keepPos); err != nil {
		return 0, fmt.Errorf("prune stale slots: %w", err)
	}
	return len(t.Exercises), nil
}

// nonNil turns a nil slice into an empty one so text[] columns get '{}' rather than NULL.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
