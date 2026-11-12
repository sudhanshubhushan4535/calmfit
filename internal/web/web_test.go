package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/seed"
)

// html/template escapes contextually at execution time, so a script it can't parse only fails when served.
func TestIndexRenders(t *testing.T) {
	rec := httptest.NewRecorder()
	Index("client-123")(rec, httptest.NewRequest("GET", "/app", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `data-client_id="client-123"`) || !strings.Contains(body, "/workouts/start") ||
		!strings.Contains(body, "/progress/weekly") || !strings.Contains(body, "/voice/parse") || !strings.Contains(body, "stt.jsonl") || strings.Contains(body, "<script src=\"https://cdn") ||
		!strings.Contains(body, "</html>") {
		t.Fatalf("status %d, body %d bytes: missing client id, workout script or closing tag", rec.Code, len(body))
	}
}

var (
	headRe   = regexp.MustCompile(`(?s)<head>.*</head>`)
	verifyRe = regexp.MustCompile(`<mark class="verify">[^<]*<sup>` + VerifyNote + `</sup></mark>`)
	tagRe    = regexp.MustCompile(`<[^>]*>`)
	digitRe  = regexp.MustCompile(`[0-9]`)
)

// Every figure on the guide must carry the "verify before launch" mark: after removing marked
// figures and all tags, no digit may remain in the page text.
func TestProteinGuideMarksEveryNumber(t *testing.T) {
	rec := httptest.NewRecorder()
	ProteinGuide(rec, httptest.NewRequest("GET", "/guide/protein", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "</html>") {
		t.Fatalf("status %d", rec.Code)
	}
	for _, topic := range []string{"Paneer", "Soya chunks, <strong>dry</strong>", "<strong>cooked</strong>", "Dal", "Curd", "Whey",
		"Low", "Moderate", "Protein-rich", "Less gravy", "most of the oil sits in the gravy", "Fat is not the enemy",
		"fiber supplement for days without fruit or vegetables, not a replacement for them"} {
		if !strings.Contains(body, topic) {
			t.Errorf("guide is missing %q", topic)
		}
	}
	marks := verifyRe.FindAllString(body, -1)
	if len(marks) < 15 {
		t.Fatalf("only %d verify marks", len(marks))
	}
	text := tagRe.ReplaceAllString(verifyRe.ReplaceAllString(headRe.ReplaceAllString(body, ""), ""), "")
	if m := digitRe.FindAllStringIndex(text, -1); len(m) > 0 {
		t.Fatalf("unmarked number near %q", text[max(0, m[0][0]-30):min(len(text), m[0][1]+30)])
	}
}

// Every image path in the seed must be served from our own embedded static folder as a JPEG.
func TestSeedImagesAreEmbedded(t *testing.T) {
	b, err := os.ReadFile("../../seed/exercises.json")
	if err != nil {
		t.Fatal(err)
	}
	var exercises []seed.Exercise
	if err := json.Unmarshal(b, &exercises); err != nil {
		t.Fatal(err)
	}
	h := Static()
	for _, e := range exercises {
		if len(e.ImagePaths) != seed.ImagesPerExercise {
			t.Errorf("%s: %d images", e.ID, len(e.ImagePaths))
		}
		for _, p := range e.ImagePaths {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
			if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || !bytes.HasPrefix(rec.Body.Bytes(), []byte{0xFF, 0xD8, 0xFF}) {
				t.Errorf("%s: status %d, type %q", p, rec.Code, rec.Header().Get("Content-Type"))
			}
		}
	}
}
