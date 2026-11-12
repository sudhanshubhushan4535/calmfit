package events

import (
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	const ok = `{"event_id":"0b6f3c1e-4c2a-4d7e-9a51-2f0e6d1c9b11","type":"plate.checked","version":1,
		"user_id":"7d9c1f20-3b4a-4e5f-8a6b-1c2d3e4f5a6b","occurred_at":"2026-11-12T09:00:00Z","data":{}}`
	if _, err := Decode([]byte(ok)); err != nil {
		t.Fatalf("valid envelope: %v", err)
	}

	for name, tc := range map[string]struct{ in, want string }{
		"not json":     {`{`, "decode envelope"},
		"bad event_id": {strings.Replace(ok, "0b6f3c1e", "XYZ", 1), "event_id"},
		"bad user_id":  {strings.Replace(ok, `"7d9c1f20-`, `"`, 1), "user_id"},
		"no type":      {strings.Replace(ok, `"plate.checked"`, `""`, 1), "type is empty"},
		"version 0":    {strings.Replace(ok, `"version":1`, `"version":0`, 1), "version 0"},
		"no time":      {strings.Replace(ok, `"occurred_at":"2026-11-12T09:00:00Z",`, ``, 1), "occurred_at"},
		"no data":      {strings.Replace(ok, `,"data":{}`, ``, 1), "data is missing"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
