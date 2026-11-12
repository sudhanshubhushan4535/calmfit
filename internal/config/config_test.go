package config

import (
	"strings"
	"testing"
	"time"
)

func TestRepoConfigIsValid(t *testing.T) {
	c, err := Load("../../config/app.json")
	if err != nil {
		t.Fatal(err)
	}
	if c.Plate.MaxPoints() != 9 || c.Plate.GoodDayMin != 6 || c.Timezone != "Asia/Kolkata" {
		t.Fatalf("unexpected repo config: %+v", c)
	}
	// Voice ships off (D36) on the cheap model with the agreed limit (D33, D34).
	if c.Voice != (Voice{Enabled: false, Model: "claude-haiku-5-5", DailyLimit: 30}) {
		t.Fatalf("unexpected repo voice config: %+v", c.Voice)
	}
}

func TestParseRejectsBadConfig(t *testing.T) {
	for name, doc := range map[string]string{
		"no tz":                `{"plate":{"protein_points":2,"fiber_points":1,"good_day_min":6}}`,
		"bad tz":               `{"timezone":"Mars/Base","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6}}`,
		"zero points":          `{"timezone":"UTC","plate":{"protein_points":0,"fiber_points":1,"good_day_min":1}}`,
		"min > max":            `{"timezone":"UTC","plate":{"protein_points":2,"fiber_points":1,"good_day_min":10}}`,
		"min zero":             `{"timezone":"UTC","plate":{"protein_points":2,"fiber_points":1,"good_day_min":0}}`,
		"unknown":              `{"timezone":"UTC","typo":1,"plate":{"protein_points":2,"fiber_points":1,"good_day_min":6}}`,
		"voice on, no model":   `{"timezone":"UTC","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6},"voice":{"enabled":true,"daily_limit":30}}`,
		"voice on, no limit":   `{"timezone":"UTC","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6},"voice":{"enabled":true,"model":"m"}}`,
		"voice off, bad limit": `{"timezone":"UTC","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6},"voice":{"model":"m","daily_limit":-1}}`,
		"voice limit too big":  `{"timezone":"UTC","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6},"voice":{"model":"m","daily_limit":1001}}`,
		"voice unknown key":    `{"timezone":"UTC","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6},"voice":{"model":"m","daily_limit":3,"key":"x"}}`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestVoiceMayBeOmitted(t *testing.T) {
	c, err := Parse([]byte(`{"timezone":"UTC","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Voice.Enabled {
		t.Fatal("voice must default to disabled")
	}
}

func TestTodayAndWeekStart(t *testing.T) {
	c, err := Parse([]byte(`{"timezone":"Asia/Kolkata","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6}}`))
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-15 19:00 UTC is 2026-11-16 00:30 IST, a Monday.
	now := time.Date(2026, 11, 15, 19, 0, 0, 0, time.UTC)
	today := c.Today(now)
	if got := today.Format(time.DateOnly); got != "2026-11-16" {
		t.Fatalf("today = %s", got)
	}
	if got := WeekStart(today).Format(time.DateOnly); got != "2026-11-16" {
		t.Fatalf("week start of Monday = %s", got)
	}
	sun := today.AddDate(0, 0, -1)
	if got := WeekStart(sun).Format(time.DateOnly); got != "2026-11-09" {
		t.Fatalf("week start of Sunday = %s", got)
	}
	if got := c.StartOf(today).UTC().Format(time.RFC3339); !strings.HasPrefix(got, "2026-11-15T18:30") {
		t.Fatalf("start of local day = %s", got)
	}
}
