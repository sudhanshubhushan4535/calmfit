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
