package main

import (
	"strings"
	"testing"
)

func TestCheckTestAuth(t *testing.T) {
	cases := []struct {
		appEnv, testAuth string
		on               bool
		errHas           string
	}{
		// The required proof: production + test auth never starts.
		{"production", "1", false, "APP_ENV=production"},
		{"production", "true", false, "must be 1"},
		{"production", "", false, ""},
		{"", "1", false, "needs APP_ENV=dev"},
		{"staging", "1", false, "needs APP_ENV=dev"},
		{"Dev", "1", false, "needs APP_ENV=dev"},
		{"dev", "", false, ""},
		{"dev", "0", false, "must be 1"},
		{"dev", "1", true, ""},
	}
	for _, c := range cases {
		on, err := checkTestAuth(c.appEnv, c.testAuth)
		if on != c.on {
			t.Errorf("APP_ENV=%q TEST_AUTH=%q: on=%v, want %v", c.appEnv, c.testAuth, on, c.on)
		}
		switch {
		case c.errHas == "" && err != nil:
			t.Errorf("APP_ENV=%q TEST_AUTH=%q: unexpected error %v", c.appEnv, c.testAuth, err)
		case c.errHas != "" && (err == nil || !strings.Contains(err.Error(), c.errHas)):
			t.Errorf("APP_ENV=%q TEST_AUTH=%q: error %v, want one containing %q", c.appEnv, c.testAuth, err, c.errHas)
		}
	}
}
