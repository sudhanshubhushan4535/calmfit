package auth

import "testing"

func TestIsLocalhost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost:8080", true},
		{"localhost", true},
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"[::1]", true},
		{"calmfit.app", false},
		{"calmfit.app:443", false},
		{"localhost.evil.com", false},
	}
	for _, tt := range tests {
		if got := isLocalhost(tt.host); got != tt.want {
			t.Errorf("isLocalhost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}
