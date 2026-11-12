package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/web"
)

// These routes never touch the database, so the router is built without one.
func router(t *testing.T) http.Handler {
	t.Helper()
	cfg, err := config.Load("../../config/app.json")
	if err != nil {
		t.Fatal(err)
	}
	return New(nil, auth.GoogleVerifier{ClientID: "client-123"}, "client-123", cfg)
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

// Every precached shell path is public (no session needed) and is not a JSON API response, so the
// service worker can never store user data (D44).
func TestShellPathsArePublicNonAPI(t *testing.T) {
	h := router(t)
	for _, p := range web.ShellPaths {
		rec := get(h, p)
		ct := rec.Header().Get("Content-Type")
		if rec.Code != 200 || strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: status %d, type %q", p, rec.Code, ct)
		}
	}
}

// The API paths the app calls must not be in the shell, and they answer without a session with a
// non-cacheable 401.
func TestAPIIsNotShellAndNotCacheable(t *testing.T) {
	h := router(t)
	shell := strings.Join(web.ShellPaths, " ")
	for _, p := range []string{"/me", "/workouts/next", "/review/weekly", "/progress/weekly", "/plate", "/me/export"} {
		if strings.Contains(" "+shell+" ", " "+p+" ") {
			t.Errorf("%s is in the shell", p)
		}
		rec := get(h, p)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: status %d, Cache-Control %q", p, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestServiceWorkerAndManifest(t *testing.T) {
	h := router(t)
	sw := get(h, "/sw.js")
	if sw.Code != 200 || !strings.HasPrefix(sw.Header().Get("Content-Type"), "text/javascript") ||
		sw.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("/sw.js: status %d, headers %v", sw.Code, sw.Header())
	}
	shell, _ := json.Marshal(web.ShellPaths)
	body := sw.Body.String()
	if !strings.Contains(body, "const SHELL = "+string(shell)+";") || strings.Contains(body, "{{") {
		t.Fatalf("sw.js does not carry the shell list:\n%s", body)
	}
	// Only GETs are ever answered by the worker.
	if !strings.Contains(body, "if (req.method !== 'GET') return;") {
		t.Fatal("sw.js must ignore non-GET requests")
	}

	m := get(h, "/manifest.webmanifest")
	if m.Code != 200 || m.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifest: status %d, type %q", m.Code, m.Header().Get("Content-Type"))
	}
	var man struct {
		ShortName string `json:"short_name"`
		StartURL  string `json:"start_url"`
		Scope     string `json:"scope"`
		Display   string `json:"display"`
		Icons     []struct{ Src, Sizes, Purpose string }
	}
	if err := json.NewDecoder(m.Body).Decode(&man); err != nil {
		t.Fatal(err)
	}
	if man.ShortName != "CalmFit" || man.StartURL != "/app" || man.Scope != "/" || man.Display != "standalone" {
		t.Fatalf("manifest fields: %+v", man)
	}
	// Chrome's installability needs a 192 and a 512 icon; every icon must be served.
	sizes := map[string]bool{}
	for _, ic := range man.Icons {
		sizes[ic.Sizes] = true
		if rec := get(h, ic.Src); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
			t.Errorf("icon %s: status %d", ic.Src, rec.Code)
		}
	}
	if !sizes["192x192"] || !sizes["512x512"] {
		t.Fatalf("icon sizes: %v", sizes)
	}
}

func TestAppPageIsInstallable(t *testing.T) {
	h := router(t)
	body := get(h, "/app").Body.String()
	for _, want := range []string{`rel="manifest" href="/manifest.webmanifest"`, `rel="apple-touch-icon"`,
		`src="/static/pwa.js"`, `id="install"`, "Add to Home Screen", "Install app"} {
		if !strings.Contains(body, want) {
			t.Errorf("/app is missing %q", want)
		}
	}
}

var externalRe = regexp.MustCompile(`<(script|link)[^>]+(src|href)="(https?:)?//`)

// The landing page is mobile-first and light: no third-party scripts, stylesheets or fonts.
func TestLandingPage(t *testing.T) {
	rec := get(router(t), "/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/: status %d", rec.Code)
	}
	for _, want := range []string{"<h1>Stop counting grams. Start getting stronger.</h1>", `name="viewport"`,
		`href="/app"`, `href="/guide/protein"`, `rel="manifest"`, `id="install"`, "Push, Pull, Legs"} {
		if !strings.Contains(body, want) {
			t.Errorf("/ is missing %q", want)
		}
	}
	if m := externalRe.FindString(body); m != "" {
		t.Errorf("external resource on the landing page: %s", m)
	}
	if len(body) > 16<<10 {
		t.Errorf("landing page is %d bytes; keep it under 16 KB", len(body))
	}
}
