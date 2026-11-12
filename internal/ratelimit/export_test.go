package ratelimit

import "time"

// SetNow replaces d's clock (tests only).
func SetNow(d *Daily, now func() time.Time) { d.now = now }
