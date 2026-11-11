package progression

import (
	"fmt"
	"strconv"
	"strings"
)

// kg formats grams as kilograms without trailing zeros: 2500 -> "2.5", 5000 -> "5".
func kg(grams int) string {
	return strconv.FormatFloat(float64(grams)/1000, 'f', -1, 64)
}

// joinInts formats rep targets as "12/10/8".
func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, "/")
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
