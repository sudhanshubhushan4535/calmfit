// Package web serves the bare manual-test page for Google sign-in and the static guide pages.
package web

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
)

//go:embed index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

// Index renders the sign-in test page with the OAuth client ID injected.
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
