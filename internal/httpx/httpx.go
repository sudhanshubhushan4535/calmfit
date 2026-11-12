// Package httpx holds tiny JSON response helpers shared by the HTTP handlers.
package httpx

import (
	"encoding/json"
	"log"
	"mime"
	"net/http"
)

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	// API responses are per user and must never be stored by a browser or proxy cache (D44).
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes {"error": msg}.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// InternalError logs the cause and returns a detail-free 500.
func InternalError(w http.ResponseWriter, op string, err error) {
	log.Printf("%s: %v", op, err)
	WriteError(w, http.StatusInternalServerError, "internal error")
}

// DecodeJSON enforces a JSON content type (cross-site callers would need a CORS preflight), caps
// the body at maxBytes and rejects unknown fields. On failure it writes the error and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, v any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		WriteError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}
