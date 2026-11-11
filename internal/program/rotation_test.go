package program

import (
	"slices"
	"testing"
)

func TestNextDayIndex(t *testing.T) {
	tests := []struct {
		name    string
		last, n int
		want    int
	}{
		{"nothing completed starts at 0", -1, 3, 0},
		{"after day 0", 0, 3, 1},
		{"after day 1", 1, 3, 2},
		{"after last day wraps", 2, 3, 0},
		{"single-day template, fresh", -1, 1, 0},
		{"single-day template, repeats", 0, 1, 0},
		{"stale index beyond a shortened template stays in range", 5, 3, 0},
		{"six-day template wraps", 5, 6, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextDayIndex(tt.last, tt.n); got != tt.want {
				t.Fatalf("NextDayIndex(%d, %d) = %d, want %d", tt.last, tt.n, got, tt.want)
			}
		})
	}
}

func TestRepTargets(t *testing.T) {
	tests := []struct {
		scheme  string
		want    []int
		wantErr bool
	}{
		{"standard", []int{12, 10, 8}, false},
		{"high_rep", []int{18, 15, 12}, false},
		{"bogus", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.scheme, func(t *testing.T) {
			got, err := RepTargets(tt.scheme)
			if (err != nil) != tt.wantErr || !slices.Equal(got, tt.want) {
				t.Fatalf("RepTargets(%q) = %v, %v; want %v, err=%v", tt.scheme, got, err, tt.want, tt.wantErr)
			}
		})
	}
}
