package voice

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
)

func w(kg float64) *float64 { return &kg }

func targets(kgs ...int) []progression.Target {
	ts := make([]progression.Target, len(kgs))
	for i, g := range kgs {
		ts[i] = progression.Target{WeightG: g, Reps: 12 - 2*i}
	}
	return ts
}

// Today's push day, trimmed: incline press fresh, lateral raise with set 1 logged, pec deck
// still waiting for its cold-start answer.
var push = []Exercise{
	{ID: "Incline_Dumbbell_Press", Remaining: targets(20000, 22500, 25000)},
	{ID: "Side_Lateral_Raise", Logged: 1, Remaining: targets(7500, 7500)},
	{ID: "Butterfly", NeedsUsualWeight: true},
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		p    Parsed
		hint string
		want Result
	}{
		{
			name: "one weight said applies to every set (D31)",
			p:    Parsed{ExerciseID: "Incline_Dumbbell_Press", Unit: KG, Sets: []ParsedSet{{w(60), 8}, {nil, 8}, {nil, 7}}},
			want: Result{ExerciseID: "Incline_Dumbbell_Press", Warnings: []string{}, Sets: []Set{
				{1, 60000, 8, SourceSaid}, {2, 60000, 8, SourceSaid}, {3, 60000, 7, SourceSaid},
			}},
		},
		{
			name: "per-set weights, the parser repeating the weight works the same",
			p:    Parsed{ExerciseID: "Incline_Dumbbell_Press", Unit: KG, Sets: []ParsedSet{{w(20), 12}, {w(22.5), 10}, {w(25), 8}}},
			want: Result{ExerciseID: "Incline_Dumbbell_Press", Warnings: []string{}, Sets: []Set{
				{1, 20000, 12, SourceSaid}, {2, 22500, 10, SourceSaid}, {3, 25000, 8, SourceSaid},
			}},
		},
		{
			name: "a new weight mid-phrase carries forward from there",
			p:    Parsed{ExerciseID: "Incline_Dumbbell_Press", Unit: KG, Sets: []ParsedSet{{w(20), 12}, {nil, 10}, {w(25), 8}}},
			want: Result{ExerciseID: "Incline_Dumbbell_Press", Warnings: []string{}, Sets: []Set{
				{1, 20000, 12, SourceSaid}, {2, 20000, 10, SourceSaid}, {3, 25000, 8, SourceSaid},
			}},
		},
		{
			name: "no weight said uses the plan's ascending targets",
			p:    Parsed{ExerciseID: "Incline_Dumbbell_Press", Sets: []ParsedSet{{nil, 12}, {nil, 10}, {nil, 8}}},
			want: Result{ExerciseID: "Incline_Dumbbell_Press", Warnings: []string{}, Sets: []Set{
				{1, 20000, 12, SourcePlan}, {2, 22500, 10, SourcePlan}, {3, 25000, 8, SourcePlan},
			}},
		},
		{
			name: "weight said only from set 2: set 1 keeps the plan",
			p:    Parsed{ExerciseID: "Incline_Dumbbell_Press", Unit: KG, Sets: []ParsedSet{{nil, 12}, {w(24), 10}}},
			want: Result{ExerciseID: "Incline_Dumbbell_Press", Warnings: []string{}, Sets: []Set{
				{1, 20000, 12, SourcePlan}, {2, 24000, 10, SourceSaid},
			}},
		},
		{
			name: "resumes after logged sets, using the in-session remaining targets",
			p:    Parsed{ExerciseID: "Side_Lateral_Raise", Sets: []ParsedSet{{nil, 9}, {nil, 10}}},
			want: Result{ExerciseID: "Side_Lateral_Raise", Warnings: []string{}, Sets: []Set{
				{2, 7500, 9, SourcePlan}, {3, 7500, 10, SourcePlan},
			}},
		},
		{
			name: "pounds are converted and rounded to 0.1 kg",
			p:    Parsed{ExerciseID: "Incline_Dumbbell_Press", Unit: LB, Sets: []ParsedSet{{w(45), 12}}},
			want: Result{ExerciseID: "Incline_Dumbbell_Press", Warnings: []string{"converted from lb to kg (rounded to 0.1 kg)"}, Sets: []Set{
				{1, 20400, 12, SourceSaid},
			}},
		},
		{
			name: "fractional kg kept to the gram",
			p:    Parsed{ExerciseID: "Incline_Dumbbell_Press", Unit: KG, Sets: []ParsedSet{{w(12.25), 12}}},
			want: Result{ExerciseID: "Incline_Dumbbell_Press", Warnings: []string{}, Sets: []Set{
				{1, 12250, 12, SourceSaid},
			}},
		},
		{
			name: "no exercise said: the open exercise is used, with a warning",
			p:    Parsed{Unit: KG, Sets: []ParsedSet{{w(8), 10}}},
			hint: "Side_Lateral_Raise",
			want: Result{ExerciseID: "Side_Lateral_Raise", Warnings: []string{"exercise taken from the open screen"}, Sets: []Set{
				{2, 8000, 10, SourceSaid},
			}},
		},
		{
			name: "an exercise said wins over the open one",
			p:    Parsed{ExerciseID: "Incline_Dumbbell_Press", Sets: []ParsedSet{{nil, 12}}},
			hint: "Side_Lateral_Raise",
			want: Result{ExerciseID: "Incline_Dumbbell_Press", Warnings: []string{}, Sets: []Set{
				{1, 20000, 12, SourcePlan},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.p, push, tt.hint)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestResolveRejects(t *testing.T) {
	inc := "Incline_Dumbbell_Press"
	tests := []struct {
		name string
		p    Parsed
		hint string
		code string
	}{
		{"no exercise and nothing open", Parsed{Sets: []ParsedSet{{nil, 8}}}, "", CodeNoExercise},
		{"exercise not in today's workout", Parsed{ExerciseID: "Barbell_Squat", Sets: []ParsedSet{{nil, 8}}}, "", CodeNotInWorkout},
		{"hint not in today's workout", Parsed{Sets: []ParsedSet{{nil, 8}}}, "Barbell_Squat", CodeNotInWorkout},
		{"cold start not answered", Parsed{ExerciseID: "Butterfly", Unit: KG, Sets: []ParsedSet{{w(40), 12}}}, "", CodeNeedsUsualWeight},
		{"no sets", Parsed{ExerciseID: inc}, "", CodeNoSets},
		{"four sets", Parsed{ExerciseID: inc, Sets: []ParsedSet{{nil, 8}, {nil, 8}, {nil, 8}, {nil, 8}}}, "", CodeTooManySets},
		{"more sets than left after logged ones", Parsed{ExerciseID: "Side_Lateral_Raise", Sets: []ParsedSet{{nil, 8}, {nil, 8}, {nil, 8}}}, "", CodeTooManySets},
		{"zero reps", Parsed{ExerciseID: inc, Sets: []ParsedSet{{nil, 0}}}, "", CodeInvalidReps},
		{"huge reps", Parsed{ExerciseID: inc, Sets: []ParsedSet{{nil, 99}}}, "", CodeInvalidReps},
		{"negative weight", Parsed{ExerciseID: inc, Unit: KG, Sets: []ParsedSet{{w(-20), 8}}}, "", CodeInvalidWeight},
		{"zero weight", Parsed{ExerciseID: inc, Unit: KG, Sets: []ParsedSet{{w(0), 8}}}, "", CodeInvalidWeight},
		{"weight over 1000 kg", Parsed{ExerciseID: inc, Unit: KG, Sets: []ParsedSet{{w(1000.5), 8}}}, "", CodeInvalidWeight},
		{"NaN weight", Parsed{ExerciseID: inc, Unit: KG, Sets: []ParsedSet{{w(math.NaN()), 8}}}, "", CodeInvalidWeight},
		{"weight rounding to 0 lb-grams", Parsed{ExerciseID: inc, Unit: LB, Sets: []ParsedSet{{w(0.1), 8}}}, "", CodeInvalidWeight},
		{"unknown unit", Parsed{ExerciseID: inc, Unit: "stone", Sets: []ParsedSet{{w(3), 8}}}, "", CodeInvalidUnit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.p, push, tt.hint)
			e, ok := IsUserError(err)
			if !ok {
				t.Fatalf("want a user error %q, got %v", tt.code, err)
			}
			if e.Code != tt.code {
				t.Errorf("code = %q, want %q (%v)", e.Code, tt.code, err)
			}
		})
	}
}

// A workout view missing targets is a server bug, not the user's fault.
func TestResolveMissingTargetsIsInternal(t *testing.T) {
	broken := []Exercise{{ID: "X", Remaining: targets(10000)}}
	_, err := Resolve(Parsed{ExerciseID: "X", Sets: []ParsedSet{{nil, 8}, {nil, 8}}}, broken, "")
	if err == nil {
		t.Fatal("want an error")
	}
	if _, ok := IsUserError(err); ok {
		t.Errorf("want an internal error, got user error %v", err)
	}
}

func TestFakeParser(t *testing.T) {
	want := Parsed{ExerciseID: "X", Sets: []ParsedSet{{nil, 8}}}
	f := &FakeParser{Results: map[string]Parsed{"x 8": want}}
	cands := []Candidate{{ID: "X", Name: "Ex"}}

	got, err := f.Parse(context.Background(), "x 8", cands)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("known transcript: got %+v, %v", got, err)
	}
	if _, err := f.Parse(context.Background(), "hello", cands); !errors.Is(err, ErrCouldNotParse) {
		t.Errorf("unknown transcript: err = %v, want ErrCouldNotParse", err)
	}
	f.Err = errors.New("provider down")
	if _, err := f.Parse(context.Background(), "x 8", cands); err == nil || errors.Is(err, ErrCouldNotParse) {
		t.Errorf("Err set: err = %v, want the provider error", err)
	}
	if n := len(f.Calls()); n != 3 {
		t.Errorf("calls = %d, want 3", n)
	}
}
