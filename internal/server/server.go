// Package server builds the full HTTP router, shared by cmd/api and integration tests.
package server

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/plate"
	"github.com/sudhanshubhushan4535/calmfit/internal/program"
	"github.com/sudhanshubhushan4535/calmfit/internal/web"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

// New returns the app's router.
func New(db *pgxpool.Pool, v auth.Verifier, clientID string, cfg config.Config) http.Handler {
	mux := http.NewServeMux()
	a := &auth.Handler{DB: db, Verifier: v}
	a.Register(mux)
	(&program.Handler{DB: db}).Register(mux, a.RequireUser)
	(&workout.Handler{DB: db}).Register(mux, a.RequireUser)
	(&plate.Handler{DB: db, Cfg: cfg}).Register(mux, a.RequireUser)
	mux.HandleFunc("GET /{$}", web.Index(clientID))
	return mux
}
