// Package config loads the app's tunable product settings (config/app.json), so values like the
// plate-check weights and the local timezone live in config, not code.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// MaxVoiceDailyLimit caps voice.daily_limit so a typo can't remove the cost guard.
const MaxVoiceDailyLimit = 1000

// MealsPerDay is how many meals a plate check scores (breakfast, lunch, dinner).
const MealsPerDay = 3

// Plate holds the plate-check scoring weights.
type Plate struct {
	ProteinPoints int `json:"protein_points"`
	FiberPoints   int `json:"fiber_points"`
	GoodDayMin    int `json:"good_day_min"` // a day with at least this many points is a good day
}

// MaxPoints is the best possible day: every meal has protein and fiber.
func (p Plate) MaxPoints() int { return MealsPerDay * (p.ProteinPoints + p.FiberPoints) }

// Voice holds the voice-logging settings. The API key is a secret and lives in .env, not here.
type Voice struct {
	// Enabled is the feature flag; it stays false until the eval accuracy is acceptable (D36).
	Enabled bool `json:"enabled"`
	// Model is the LLM that extracts sets from a transcript.
	Model string `json:"model"`
	// DailyLimit is how many parses one user may run per local day.
	DailyLimit int `json:"daily_limit"`
}

// Config is config/app.json.
type Config struct {
	// Timezone is the IANA zone used for the user's local date and Mon–Sun weeks (no per-user zone in v1).
	Timezone string `json:"timezone"`
	Plate    Plate  `json:"plate"`
	// Voice may be left out, which means disabled.
	Voice Voice `json:"voice"`

	Location *time.Location `json:"-"`
}

// Load reads and validates the config file at path.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return Parse(b)
}

// Parse decodes and validates a config document; unknown fields are rejected so typos fail loudly.
func Parse(b []byte) (Config, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config: decode: %w", err)
	}
	if c.Timezone == "" {
		return Config{}, errors.New("config: timezone is required")
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return Config{}, fmt.Errorf("config: timezone: %w", err)
	}
	c.Location = loc
	p := c.Plate
	if p.ProteinPoints <= 0 || p.FiberPoints <= 0 {
		return Config{}, errors.New("config: plate protein_points and fiber_points must be > 0")
	}
	if p.GoodDayMin <= 0 || p.GoodDayMin > p.MaxPoints() {
		return Config{}, fmt.Errorf("config: plate good_day_min must be 1–%d", p.MaxPoints())
	}
	if v := c.Voice; v.Enabled || v.Model != "" || v.DailyLimit != 0 {
		if v.Model == "" {
			return Config{}, errors.New("config: voice model is required")
		}
		if v.DailyLimit < 1 || v.DailyLimit > MaxVoiceDailyLimit {
			return Config{}, fmt.Errorf("config: voice daily_limit must be 1–%d", MaxVoiceDailyLimit)
		}
	}
	return c, nil
}

// Today returns the current local date (midnight UTC of the local calendar day; see Date).
func (c Config) Today(now time.Time) time.Time { return Date(now.In(c.Location)) }

// Date returns t's calendar day (in t's own location) as midnight UTC, so dates compare and
// add days without DST or zone surprises.
func Date(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// WeekStart returns the Monday of the Mon–Sun week containing date (a Date value).
func WeekStart(date time.Time) time.Time {
	offset := (int(date.Weekday()) + 6) % 7 // Monday = 0
	return date.AddDate(0, 0, -offset)
}

// StartOf returns the instant local midnight begins on date (a Date value).
func (c Config) StartOf(date time.Time) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, c.Location)
}
