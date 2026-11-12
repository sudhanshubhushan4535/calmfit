package workout

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
	"github.com/sudhanshubhushan4535/calmfit/internal/ratelimit"
	"github.com/sudhanshubhushan4535/calmfit/internal/voice"
)

// maxTranscriptRunes caps a transcript: a set log is a few words, and the cap bounds LLM cost.
const maxTranscriptRunes = 200

// Error codes of the voice endpoint (D35), besides voice's own 422 codes.
const (
	errTranscript       = "transcript must be 1–200 characters"
	errVoiceLimit       = "voice_daily_limit"
	errVoiceUnavailable = "voice_unavailable"
	errVoiceParser      = "voice_parser_unavailable"
)

// Limiter counts a voice parse against the user's daily allowance (ratelimit.Daily).
type Limiter interface {
	Allow(ctx context.Context, userID string) error
}

// Voice is what the voice endpoint needs; a nil *Voice on the Handler means voice is off.
type Voice struct {
	Parser  voice.Parser
	Limiter Limiter
}

// VoiceParse is the response of POST /workouts/{id}/voice/parse: sets to show on the confirm
// screen. Nothing is saved; the client logs each confirmed set with POST /workouts/{id}/sets.
type VoiceParse struct {
	ExerciseID   string      `json:"exercise_id"`
	ExerciseName string      `json:"exercise_name"`
	Sets         []voice.Set `json:"sets"`
	Warnings     []string    `json:"warnings"`
}

// voiceParse turns a transcript into sets for one-tap confirm (D35). It is read-only by design:
// it never opens a write transaction, so voice can't become a second write path.
func (h *Handler) voiceParse(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	id := r.PathValue("id")

	var body struct {
		Transcript string `json:"transcript"`
		ExerciseID string `json:"exercise_id"` // the exercise open on screen; optional
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	transcript := strings.TrimSpace(body.Transcript)
	if n := utf8.RuneCountInString(transcript); n == 0 || n > maxTranscriptRunes {
		httpx.WriteError(w, http.StatusBadRequest, errTranscript)
		return
	}

	// Ownership and state come first, so a bad request never uses up the daily allowance.
	wo, infos, err := loadWorkout(ctx, h.DB, uid, id)
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, errWorkoutNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, "voice: load workout", err)
		return
	}
	if wo.Status != statusInProgress {
		httpx.WriteError(w, http.StatusConflict, errWorkoutCompleted)
		return
	}

	switch err := h.Voice.Limiter.Allow(ctx, uid); {
	case errors.Is(err, ratelimit.ErrLimited):
		httpx.WriteError(w, http.StatusTooManyRequests, errVoiceLimit)
		return
	case err != nil: // fail closed (D34)
		log.Printf("voice: rate limiter: %v", err)
		httpx.WriteError(w, http.StatusServiceUnavailable, errVoiceUnavailable)
		return
	}

	cands, err := candidates(ctx, h.DB, wo)
	if err != nil {
		httpx.InternalError(w, "voice: candidates", err)
		return
	}
	parsed, err := h.Voice.Parser.Parse(ctx, transcript, cands)
	if err == nil {
		var view []voice.Exercise
		for _, ex := range wo.Exercises {
			view = append(view, voice.Exercise{ID: ex.ID, NeedsUsualWeight: ex.NeedsUsualWeight,
				Logged: len(ex.Sets), Remaining: ex.Remaining})
		}
		var res voice.Result
		if res, err = voice.Resolve(parsed, view, body.ExerciseID); err == nil {
			httpx.WriteJSON(w, http.StatusOK, VoiceParse{ExerciseID: res.ExerciseID,
				ExerciseName: infos[res.ExerciseID].Name, Sets: res.Sets, Warnings: res.Warnings})
			return
		}
	}
	if ue, ok := voice.IsUserError(err); ok {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": ue.Code, "message": ue.Msg})
		return
	}
	if errors.Is(err, voice.ErrProvider) {
		log.Printf("voice: parser: %v", err)
		httpx.WriteError(w, http.StatusBadGateway, errVoiceParser)
		return
	}
	httpx.InternalError(w, "voice: parse", err)
}

// candidates lists the workout's exercises with their aliases, in workout order, for the parser.
func candidates(ctx context.Context, q querier, wo Workout) ([]voice.Candidate, error) {
	ids := make([]string, len(wo.Exercises))
	for i, ex := range wo.Exercises {
		ids[i] = ex.ID
	}
	rows, err := q.Query(ctx, `SELECT id, name, aliases FROM exercises WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("load aliases: %w", err)
	}
	defer rows.Close()
	byID := map[string]voice.Candidate{}
	for rows.Next() {
		var c voice.Candidate
		if err := rows.Scan(&c.ID, &c.Name, &c.Aliases); err != nil {
			return nil, fmt.Errorf("scan aliases: %w", err)
		}
		byID[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate aliases: %w", err)
	}
	out := make([]voice.Candidate, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out, nil
}
