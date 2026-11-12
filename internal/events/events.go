// Package events defines the event envelope and payloads published to Kafka through the outbox.
// Payloads carry facts only (ids, dates, weights, reps), never email or name.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Topics. workout-events is keyed by user_id, so each user's events stay ordered within a partition.
const (
	Topic    = "workout-events"
	DLQTopic = "workout-events-dlq"
)

// Event types and the payload version this code writes.
const (
	TypeWorkoutLogged = "workout.logged"
	TypePlateChecked  = "plate.checked"
	TypeUserDeleted   = "user.deleted"

	V1 = 1
)

// Envelope is the Kafka record value. The record key is UserID.
type Envelope struct {
	EventID    string          `json:"event_id"`
	Type       string          `json:"type"`
	Version    int             `json:"version"`
	UserID     string          `json:"user_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// WorkoutLogged (v1) is written when a workout goes from in_progress to completed.
type WorkoutLogged struct {
	WorkoutID   string      `json:"workout_id"`
	Day         string      `json:"day"`
	CompletedAt time.Time   `json:"completed_at"`
	Sets        []LoggedSet `json:"sets"`
}

// LoggedSet is one working set of a logged workout.
type LoggedSet struct {
	ExerciseID string `json:"exercise_id"`
	SetNumber  int    `json:"set_number"`
	WeightG    int    `json:"weight_g"`
	Reps       int    `json:"reps"`
}

// PlateChecked (v1) is written on every plate-check upsert; it carries the meal's new state.
type PlateChecked struct {
	LocalDate string `json:"local_date"` // YYYY-MM-DD in the config timezone
	Meal      string `json:"meal"`
	Protein   bool   `json:"protein"`
	Fiber     bool   `json:"fiber"`
}

// UserDeleted (v1) has no data: the user id is in the envelope.
type UserDeleted struct{}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Decode parses and validates an envelope. Any error means the record can never be processed.
func Decode(b []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return Envelope{}, fmt.Errorf("decode envelope: %w", err)
	}
	return e, e.Validate()
}

// Validate checks the envelope fields every consumer relies on.
func (e Envelope) Validate() error {
	var errs []error
	if !uuidRE.MatchString(e.EventID) {
		errs = append(errs, fmt.Errorf("event_id %q is not a lowercase uuid", e.EventID))
	}
	if !uuidRE.MatchString(e.UserID) {
		errs = append(errs, fmt.Errorf("user_id %q is not a lowercase uuid", e.UserID))
	}
	if e.Type == "" {
		errs = append(errs, errors.New("type is empty"))
	}
	if e.Version < 1 {
		errs = append(errs, fmt.Errorf("version %d < 1", e.Version))
	}
	if e.OccurredAt.IsZero() {
		errs = append(errs, errors.New("occurred_at is missing"))
	}
	if len(e.Data) == 0 {
		errs = append(errs, errors.New("data is missing"))
	}
	return errors.Join(errs...)
}
