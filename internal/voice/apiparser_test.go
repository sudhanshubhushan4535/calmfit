package voice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// These tests never reach the real API: every parser points at an httptest server.

var pushCandidates = []Candidate{
	{ID: "Incline_Dumbbell_Press", Name: "Incline Dumbbell Press", Aliases: []string{"incline dumbell"}},
	{ID: "Side_Lateral_Raise", Name: "Dumbbell Lateral Raise", Aliases: []string{"dumbbell lateral raise"}},
}

// reply is a Messages API response whose text block is text.
func reply(stopReason, text string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "msg_test", "type": "message", "role": "assistant", "model": "claude-haiku-5-5",
		"content":     []any{map[string]any{"type": "text", "text": text}},
		"stop_reason": stopReason, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 900, "output_tokens": 60},
	})
	return string(b)
}

// fakeAPI serves handler as the Messages API and returns a parser pointed at it.
func fakeAPI(t *testing.T, handler http.HandlerFunc, opts ...option.RequestOption) *APIParser {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewAPIParser("test-key", "claude-haiku-5-5", append([]option.RequestOption{option.WithBaseURL(srv.URL)}, opts...)...)
}

func respond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestAPIParserRequestAndReply(t *testing.T) {
	var got map[string]any
	var gotPath, gotKey string
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		respond(200, reply("end_turn",
			`{"understood":true,"exercise_id":"Incline_Dumbbell_Press","other_exercise":false,"unit":"kg","sets":[{"weight":60,"reps":8},{"weight":null,"reps":8},{"weight":null,"reps":7}]}`))(w, r)
	})

	parsed, usage, err := p.ParseWithUsage(context.Background(), "incline 60, 8 8 7", pushCandidates)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Parsed{ExerciseID: "Incline_Dumbbell_Press", Unit: KG, Sets: []ParsedSet{{w(60), 8}, {nil, 8}, {nil, 7}}}
	if !reflect.DeepEqual(parsed, want) {
		t.Errorf("parsed = %+v, want %+v", parsed, want)
	}
	if usage != (Usage{Model: "claude-haiku-5-5", InputTokens: 900, OutputTokens: 60}) {
		t.Errorf("usage = %+v", usage)
	}

	if gotPath != "/v1/messages" || gotKey != "Bearer test-key" {
		t.Errorf("path %q key %q", gotPath, gotKey)
	}
	if got["model"] != "claude-haiku-5-5" || got["max_tokens"] != float64(apiMaxTokens) {
		t.Errorf("model/max_tokens = %v/%v", got["model"], got["max_tokens"])
	}
	if th, _ := got["thinking"].(map[string]any); th["type"] != "disabled" {
		t.Errorf("thinking = %v, want disabled", got["thinking"])
	}
	oc, _ := got["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	if oc["effort"] != "low" || format["type"] != "json_schema" {
		t.Errorf("output_config = %v", oc)
	}
	// exercise_id is an enum of today's ids plus "" (no exercise said).
	schema, _ := format["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	ex, _ := props["exercise_id"].(map[string]any)
	if !reflect.DeepEqual(ex["enum"], []any{"", "Incline_Dumbbell_Press", "Side_Lateral_Raise"}) {
		t.Errorf("exercise_id enum = %v", ex["enum"])
	}

	// D32: only the fixed prompt, today's exercises and the transcript leave the server.
	for k := range got {
		switch k {
		case "model", "max_tokens", "system", "messages", "thinking", "output_config":
		default:
			t.Errorf("unexpected request field %q", k)
		}
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", msgs)
	}
	body, _ := json.Marshal(msgs[0])
	for _, s := range []string{"incline 60, 8 8 7", "Incline_Dumbbell_Press", "incline dumbell"} {
		if !strings.Contains(string(body), s) {
			t.Errorf("user message lacks %q: %s", s, body)
		}
	}
}

func TestAPIParserPassesOtherExercise(t *testing.T) {
	p := fakeAPI(t, respond(200, reply("end_turn",
		`{"understood":true,"exercise_id":"","other_exercise":true,"unit":"kg","sets":[{"weight":60,"reps":5}]}`)))
	got, err := p.Parse(context.Background(), "squat 60, 5", pushCandidates)
	if err != nil || !got.OtherExercise || got.ExerciseID != "" {
		t.Fatalf("got %+v, %v; want OtherExercise", got, err)
	}
	props := outputSchema(pushCandidates)["properties"].(map[string]any)
	if _, ok := props["other_exercise"]; !ok {
		t.Error("schema lacks other_exercise")
	}
}

func TestAPIParserCouldNotParse(t *testing.T) {
	for name, body := range map[string]string{
		"not a set log": reply("end_turn", `{"understood":false,"exercise_id":"","other_exercise":false,"unit":"","sets":[]}`),
		"refusal":       reply("refusal", ``),
		"truncated":     reply("max_tokens", `{"understood":true,"exercise_id":"Incl`),
	} {
		t.Run(name, func(t *testing.T) {
			p := fakeAPI(t, respond(200, body))
			_, err := p.Parse(context.Background(), "hello", pushCandidates)
			if !errors.Is(err, ErrCouldNotParse) {
				t.Errorf("err = %v, want ErrCouldNotParse", err)
			}
		})
	}
}

func TestAPIParserOffSchemaReplyIsProviderError(t *testing.T) {
	for name, text := range map[string]string{
		"not JSON":               `sure! incline 60`,
		"other_exercise missing": `{"understood":true,"exercise_id":"","unit":"","sets":[]}`,
		"missing field":          `{"understood":true,"exercise_id":"","other_exercise":false,"sets":[]}`,
		"unknown field":          `{"understood":true,"exercise_id":"","other_exercise":false,"unit":"","sets":[],"note":"x"}`,
		"exercise not sent":      `{"understood":true,"exercise_id":"Barbell_Squat","other_exercise":false,"unit":"","sets":[{"weight":null,"reps":5}]}`,
		"unknown unit":           `{"understood":true,"exercise_id":"","other_exercise":false,"unit":"stone","sets":[{"weight":3,"reps":5}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := fakeAPI(t, respond(200, reply("end_turn", text)))
			_, err := p.Parse(context.Background(), "x", pushCandidates)
			if !errors.Is(err, ErrProvider) {
				t.Errorf("err = %v, want ErrProvider", err)
			}
		})
	}
}

// fastBudget scales the real budget down so the timing tests run in well under a second.
var fastBudget = budget{deadline: 600 * time.Millisecond, attempt: 500 * time.Millisecond,
	minRetry: 200 * time.Millisecond, backoff: 10 * time.Millisecond, maxAttempts: 2}

const overloaded = `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`

// scripted returns a parser on a fake API that answers the n-th call (1-based) with steps[n-1];
// "hang" doesn't answer until the test ends.
func scripted(t *testing.T, calls *atomic.Int32, steps ...string) *APIParser {
	release := make(chan struct{})
	p := fakeAPI(t, script(calls, release, steps...))
	// Registered after the server, so it runs before the server's Close (cleanups are LIFO).
	t.Cleanup(func() { close(release) })
	p.budget = fastBudget
	return p
}

func script(calls *atomic.Int32, release <-chan struct{}, steps ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		step := steps[min(n, len(steps))-1]
		switch step {
		case "hang":
			select {
			case <-r.Context().Done():
			case <-release:
			}
		case "503":
			respond(503, overloaded)(w, r)
		case "400":
			respond(400, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`)(w, r)
		case "ok":
			respond(200, reply("end_turn", `{"understood":true,"exercise_id":"","other_exercise":false,"unit":"","sets":[{"weight":null,"reps":8}]}`))(w, r)
		}
	}
}

func TestAPIParserRealBudget(t *testing.T) {
	p := NewAPIParser("k", "m")
	if p.budget != (budget{6 * time.Second, 5 * time.Second, 2 * time.Second, 200 * time.Millisecond, 2}) {
		t.Errorf("budget = %+v", p.budget)
	}
}

func TestAPIParserRetriesAndDeadline(t *testing.T) {
	// The retry gets the 50 ms left of a 300 ms deadline (50 ms >= 20 ms minimum), though its own
	// attempt timeout is 250 ms: only the overall deadline can end it.
	cutShort := budget{deadline: 300 * time.Millisecond, attempt: 250 * time.Millisecond,
		minRetry: 20 * time.Millisecond, backoff: 0, maxAttempts: 2}
	tests := []struct {
		name      string
		b         budget
		steps     []string
		wantCalls int32
		wantOK    bool
		minTime   time.Duration
		maxTime   time.Duration
	}{
		{"fast 5xx is retried and the retry succeeds", fastBudget, []string{"503", "ok"}, 2, true, 0, 200 * time.Millisecond},
		{"5xx twice: two attempts, then a provider error", fastBudget, []string{"503", "503"}, 2, false, 0, 200 * time.Millisecond},
		{"400 is not retried", fastBudget, []string{"400"}, 1, false, 0, 200 * time.Millisecond},
		// The first attempt uses 500 of 600 ms; 100 ms left < 200 ms minimum, so no retry.
		{"attempt timeout with too little time left: no retry", fastBudget, []string{"hang", "ok"}, 1, false, 450 * time.Millisecond, 650 * time.Millisecond},
		// Two attempts, total ≈ 300 ms: the deadline, not 2 × 250 ms.
		{"the overall deadline cuts the retry short", cutShort, []string{"hang", "hang"}, 2, false, 280 * time.Millisecond, 400 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			p := scripted(t, &calls, tt.steps...)
			p.budget = tt.b
			start := time.Now()
			_, err := p.Parse(context.Background(), "x", pushCandidates)
			took := time.Since(start)

			if tt.wantOK && err != nil {
				t.Fatalf("err = %v, want success", err)
			}
			if !tt.wantOK && !errors.Is(err, ErrProvider) {
				t.Fatalf("err = %v, want ErrProvider", err)
			}
			if n := calls.Load(); n != tt.wantCalls {
				t.Errorf("calls = %d, want %d", n, tt.wantCalls)
			}
			if took < tt.minTime || took > tt.maxTime {
				t.Errorf("took %v, want %v–%v", took, tt.minTime, tt.maxTime)
			}
		})
	}
}

func TestAPIParserCallerCancelIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	p := scripted(t, &calls, "hang", "ok")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Parse(ctx, "x", pushCandidates)
	if !errors.Is(err, ErrProvider) || calls.Load() != 1 {
		t.Errorf("err = %v, calls = %d; want ErrProvider after 1 call", err, calls.Load())
	}
}
