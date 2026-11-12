package backup

import "testing"

func TestCompare(t *testing.T) {
	want := map[string]int64{"users": 3, "workouts": 10}
	if _, ok := Compare(want, map[string]int64{"users": 3, "workouts": 10}); !ok {
		t.Error("equal counts should pass")
	}
	for name, got := range map[string]map[string]int64{
		"count differs": {"users": 3, "workouts": 9},
		"table missing": {"users": 3},
		"extra table":   {"users": 3, "workouts": 10, "surprise": 0},
	} {
		if _, ok := Compare(want, got); ok {
			t.Errorf("%s: passed", name)
		}
	}
	if _, ok := Compare(map[string]int64{}, map[string]int64{}); ok {
		t.Error("an empty manifest must not pass")
	}
	diffs, _ := Compare(want, map[string]int64{"users": 3})
	if len(diffs) != 2 || diffs[1].Table != "workouts" || diffs[1].InRestore {
		t.Errorf("diffs = %+v", diffs)
	}
}

func TestWithDatabase(t *testing.T) {
	got, err := WithDatabase("postgres://u:p@localhost:5434/calmfit?sslmode=disable", "calmfit_restore_check")
	if err != nil || got != "postgres://u:p@localhost:5434/calmfit_restore_check?sslmode=disable" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := WithDatabase("host=localhost dbname=calmfit", "x"); err == nil {
		t.Error("a keyword/value DSN should be rejected")
	}
	if n, _ := DatabaseName("postgres://u:p@h:1/calmfit_test?x=y"); n != "calmfit_test" {
		t.Errorf("DatabaseName = %q", n)
	}
}
