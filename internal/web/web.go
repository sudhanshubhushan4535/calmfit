// Package web serves the bare manual-test page for Google sign-in.
package web

import (
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
