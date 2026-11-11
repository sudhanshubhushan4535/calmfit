// Package httpx holds tiny JSON response helpers shared by the HTTP handlers.
package httpx

import (
	"encoding/json"
	"log"
	"net/http"
)

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
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
