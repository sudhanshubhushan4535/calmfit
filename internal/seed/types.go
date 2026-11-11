// Package seed holds the seed data model and pure validation rules (no DB access).
package seed

// Exercise is one catalog entry in seed/exercises.json.
type Exercise struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases"`
	Equipment      *string  `json:"equipment"` // nullable in the dataset (body-only exercises)
	PrimaryMuscles []string `json:"primary_muscles"`
	TargetMuscle   string   `json:"target_muscle"`
	MovementType   string   `json:"movement_type"`
	MuscleSize     string   `json:"muscle_size"`
	RepScheme      string   `json:"rep_scheme"`
	RepStep        int      `json:"rep_step"`
	ImagePaths     []string `json:"image_paths"`
	Instructions   []string `json:"instructions"`
}

// TemplateExercise is one slot; its position is its index among same-day slots.
type TemplateExercise struct {
	Day        string `json:"day"`
	ExerciseID string `json:"exercise_id"`
}

// Template is one entry in seed/templates.json.
type Template struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Days      []string           `json:"days"`
	Exercises []TemplateExercise `json:"exercises"`
}
