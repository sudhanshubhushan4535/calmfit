// Package program handles onboarding and picking the next workout in a template's rotation.
package program

import "fmt"

// NextDayIndex returns the index of the next day in a rotation of n days, given the index of the
// last completed day (-1 = nothing completed yet). n > 0 is guaranteed by the
// cardinality(days) > 0 CHECK on program_templates. The modulo also keeps a stale "last" in range
// if a template is ever shortened.
func NextDayIndex(last, n int) int {
	return (last + 1) % n
}

// RepTargets returns the per-set rep targets for a rep scheme.
func RepTargets(scheme string) ([]int, error) {
	switch scheme {
	case "standard":
		return []int{12, 10, 8}, nil
	case "high_rep":
		return []int{18, 15, 12}, nil
	default:
		return nil, fmt.Errorf("unknown rep scheme %q", scheme)
	}
}
