package seed

import (
	"slices"
	"strings"
	"testing"
)

func ex(id, muscle, mtype string) Exercise {
	return Exercise{ID: id, TargetMuscle: muscle, MovementType: mtype}
}

var testCatalog = map[string]Exercise{
	"squat":    ex("squat", "quads", "compound"),
	"front_sq": ex("front_sq", "quads", "compound"),
	"leg_ext":  ex("leg_ext", "quads", "isolation"),
	"bench":    ex("bench", "mid_chest", "compound"),
	"pushdown": ex("pushdown", "triceps", "isolation"),
	"leg_curl": ex("leg_curl", "hamstrings", "isolation"),
	"calf":     ex("calf", "calves", "isolation"),
	"crunch":   ex("crunch", "abs", "isolation"),
}

func tmpl(slots ...TemplateExercise) Template {
	return Template{ID: "t", Days: []string{"push", "legs"}, Exercises: slots}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    Template
		wantErr string // substring; empty means must pass
	}{
		{
			name: "valid routine passes",
			tmpl: tmpl(
				TemplateExercise{"push", "bench"},
				TemplateExercise{"push", "pushdown"},
				TemplateExercise{"legs", "squat"},
				TemplateExercise{"legs", "leg_curl"},
			),
		},
		{
			name: "same muscle and same type on one day fails",
			tmpl: tmpl(
				TemplateExercise{"legs", "squat"},
				TemplateExercise{"legs", "front_sq"},
			),
			wantErr: "two compound quads exercises: squat and front_sq",
		},
		{
			name: "same muscle and type on different days passes",
			tmpl: tmpl(
				TemplateExercise{"push", "squat"},
				TemplateExercise{"legs", "front_sq"},
			),
		},
		{
			name: "compound plus isolation on same muscle passes",
			tmpl: tmpl(
				TemplateExercise{"legs", "squat"},
				TemplateExercise{"legs", "leg_ext"},
			),
		},
		{
			name:    "unknown day fails",
			tmpl:    tmpl(TemplateExercise{"leg", "squat"}),
			wantErr: `has day "leg" not in [push legs]`,
		},
		{
			name:    "unknown exercise id fails",
			tmpl:    tmpl(TemplateExercise{"legs", "nope"}),
			wantErr: "nope (day \"legs\") is not in the exercise catalog",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.tmpl, testCatalog)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("want pass, got %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCoverage(t *testing.T) {
	tests := []struct {
		name string
		tmpl Template
		want []string
	}{
		{
			name: "reports groups with no exercise",
			tmpl: tmpl(
				TemplateExercise{"push", "bench"},
				TemplateExercise{"legs", "squat"},
			),
			want: []string{"shoulders", "back", "biceps", "triceps", "hamstrings", "glutes", "calves", "core"},
		},
		{
			name: "any tag in a group counts for it",
			tmpl: tmpl(TemplateExercise{"legs", "crunch"}),
			want: []string{"chest", "shoulders", "back", "biceps", "triceps", "quads", "hamstrings", "glutes", "calves"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Coverage(tt.tmpl, testCatalog); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func validExercise() Exercise {
	return Exercise{ID: "bench", WeightStepG: 2500, Instructions: []string{"Press."},
		Cues:       []string{"Feet flat", "Bar to mid-chest"},
		ImagePaths: []string{"/static/exercises/bench/0.jpg", "/static/exercises/bench/1.jpg"}}
}

func TestValidateExercise(t *testing.T) {
	if err := ValidateExercise(validExercise()); err != nil {
		t.Fatalf("valid exercise: %v", err)
	}
	cases := map[string]struct {
		edit    func(*Exercise)
		wantErr string
	}{
		"zero step":       {func(e *Exercise) { e.WeightStepG = 0 }, "weight_step_g must be > 0"},
		"negative step":   {func(e *Exercise) { e.WeightStepG = -2500 }, "weight_step_g must be > 0"},
		"no instructions": {func(e *Exercise) { e.Instructions = nil }, "instructions are required"},
		"one cue":         {func(e *Exercise) { e.Cues = e.Cues[:1] }, "exactly 2 cues"},
		"empty cue":       {func(e *Exercise) { e.Cues[1] = "" }, "must be 1–80"},
		"long cue":        {func(e *Exercise) { e.Cues[0] = strings.Repeat("x", 81) }, "must be 1–80"},
		"remote image":    {func(e *Exercise) { e.ImagePaths[0] = "https://example.com/0.jpg" }, "image_paths must be"},
		"one image":       {func(e *Exercise) { e.ImagePaths = e.ImagePaths[:1] }, "image_paths must be"},
	}
	for name, c := range cases {
		e := validExercise()
		c.edit(&e)
		if err := ValidateExercise(e); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, c.wantErr)
		}
	}
}
