// Package web serves the bare manual-test page for Google sign-in and the static guide pages.
package web

import (
	"bytes"
	_ "embed"
	"html/template"
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
