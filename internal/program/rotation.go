// Package program handles onboarding and the template day rotation.
package program

// NextDayIndex returns the index of the next day in a rotation of n days, given the index of the
// last completed day (-1 = nothing completed yet). n > 0 is guaranteed by the
// cardinality(days) > 0 CHECK on program_templates. The modulo also keeps a stale "last" in range
// if a template is ever shortened.
func NextDayIndex(last, n int) int {
	return (last + 1) % n
}
