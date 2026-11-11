package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// html/template escapes contextually at execution time, so a script it can't parse only fails when served.
func TestIndexRenders(t *testing.T) {
	rec := httptest.NewRecorder()
	Index("client-123")(rec, httptest.NewRequest("GET", "/", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `data-client_id="client-123"`) || !strings.Contains(body, "/workouts/start") ||
		!strings.Contains(body, "</html>") {
		t.Fatalf("status %d, body %d bytes: missing client id, workout script or closing tag", rec.Code, len(body))
	}
}
