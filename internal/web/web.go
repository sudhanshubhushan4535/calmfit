// Package web serves the landing page (/), the manual-test app page (/app), the PWA files and the static guide pages.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	texttemplate "text/template"
)

//go:embed index.html
var indexHTML string

//go:embed install.html
var installHTML string

// pageTmpl parses a page together with the shared "install" partial (the home-screen guide).
func pageTmpl(name, src string) *template.Template {
	t := template.Must(template.New("install").Parse(installHTML))
	return template.Must(t.New(name).Parse(src))
}

var indexTmpl = pageTmpl("index", indexHTML)

// Index renders the app page (the manual-test page, served at /app) with the OAuth client ID injected.
func Index(clientID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := indexTmpl.Execute(w, struct{ ClientID string }{clientID}); err != nil {
			log.Printf("web: render index: %v", err)
		}
	}
}

//go:embed static
var staticFS embed.FS

// Static serves the embedded static/ folder (exercise images) under /static/. Files are our own
// copies, never hot-linked, and are cached for a day.
func Static() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("web: static fs: " + err.Error())
	}
	files := http.StripPrefix("/static/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		files.ServeHTTP(w, r)
	})
}

// StaticFS exposes the embedded static/ folder for tests.
func StaticFS() fs.FS {
	sub, _ := fs.Sub(staticFS, "static")
	return sub
}

//go:embed protein.html
var proteinHTML string

// VerifyNote is attached to every number on the guide pages until a nutrition source has checked it.
const VerifyNote = "verify before launch"

// proteinPage is rendered once at startup: the page is static, {{v "…"}} only wraps each figure
// in a visible "verify before launch" mark.
var proteinPage = func() []byte {
	t := template.Must(template.New("protein").Funcs(template.FuncMap{
		"v": func(s string) template.HTML {
			return template.HTML(`<mark class="verify">` + template.HTMLEscapeString(s) +
				`<sup>` + VerifyNote + `</sup></mark>`)
		},
	}).Parse(proteinHTML))
	var b bytes.Buffer
	if err := t.Execute(&b, nil); err != nil {
		panic("web: render protein guide: " + err.Error())
	}
	return b.Bytes()
}()

// ProteinGuide serves the static veg protein guide.
func ProteinGuide(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(proteinPage)
}

//go:embed landing.html
var landingHTML string

// landingPage is static, so it is rendered once at startup.
var landingPage = func() []byte {
	var b bytes.Buffer
	if err := pageTmpl("landing", landingHTML).Execute(&b, nil); err != nil {
		panic("web: render landing: " + err.Error())
	}
	return b.Bytes()
}()

// Landing serves the public landing page at /.
func Landing(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(landingPage)
}

// ShellPaths is the app shell the service worker precaches (D44). Only pages and static files that
// are the same for every user belong here; never an API path. Tests enforce both.
var ShellPaths = []string{
	"/",
	"/app",
	"/manifest.webmanifest",
	"/static/pwa.js",
	"/static/icons/icon-192.png",
	"/static/icons/icon-512.png",
	"/static/icons/maskable-512.png",
	"/static/icons/apple-touch-icon.png",
}

//go:embed manifest.webmanifest
var manifest []byte

// Manifest serves the web app manifest.
func Manifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(manifest)
}

//go:embed sw.js
var swJS string

var swTmpl = texttemplate.Must(texttemplate.New("sw").Parse(swJS))

// shellVersion hashes every embedded shell source plus the client ID, so any change to the shell
// gives the worker a new cache name and old caches are dropped on activate.
func shellVersion(clientID string) string {
	h := sha256.New()
	for _, s := range []string{indexHTML, landingHTML, installHTML, string(manifest), swJS, clientID} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	for _, p := range ShellPaths {
		if rest, ok := strings.CutPrefix(p, "/static/"); ok {
			b, err := fs.ReadFile(staticFS, "static/"+rest)
			if err != nil {
				panic("web: shell file missing: " + p)
			}
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ServiceWorker serves /sw.js. It must sit at the root to control scope "/", and it is never
// cached by the browser's HTTP cache, so a deploy reaches phones on their next visit.
func ServiceWorker(clientID string) http.HandlerFunc {
	version, _ := json.Marshal(shellVersion(clientID))
	shell, _ := json.Marshal(ShellPaths)
	var b bytes.Buffer
	if err := swTmpl.Execute(&b, struct{ Version, Shell string }{string(version), string(shell)}); err != nil {
		panic("web: render sw.js: " + err.Error())
	}
	body := b.Bytes()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	}
}
