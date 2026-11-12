// Package server builds the full HTTP router, shared by cmd/api and integration tests.
package server

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/account"
	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/exercise"
	"github.com/sudhanshubhushan4535/calmfit/internal/plate"
	"github.com/sudhanshubhushan4535/calmfit/internal/program"
	"github.com/sudhanshubhushan4535/calmfit/internal/voice"
	"github.com/sudhanshubhushan4535/calmfit/internal/web"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

// Option configures optional parts of the router.
type Option func(*options)

type options struct {
	voice *workout.Voice
}

// WithVoice supplies the voice parser and rate limiter. The voice route exists only when
// cfg.Voice.Enabled is true and this option is given; otherwise it is a plain 404 (D35).
func WithVoice(p voice.Parser, l workout.Limiter) Option {
	return func(o *options) { o.voice = &workout.Voice{Parser: p, Limiter: l} }
}

// New returns the app's router.
func New(db *pgxpool.Pool, v auth.Verifier, clientID string, cfg config.Config, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if !cfg.Voice.Enabled {
		o.voice = nil
	}
	mux := http.NewServeMux()
	a := &auth.Handler{DB: db, Verifier: v}
	a.Register(mux)
	(&program.Handler{DB: db}).Register(mux, a.RequireUser)
	(&workout.Handler{DB: db, Cfg: cfg, Voice: o.voice}).Register(mux, a.RequireUser)
	(&plate.Handler{DB: db, Cfg: cfg}).Register(mux, a.RequireUser)
	(&exercise.Handler{DB: db}).Register(mux, a.RequireUser)
	(&account.Handler{DB: db}).Register(mux, a.RequireUser)
	mux.HandleFunc("GET /{$}", web.Landing)
	mux.HandleFunc("GET /app", web.Index(clientID))
	mux.HandleFunc("GET /manifest.webmanifest", web.Manifest)
	mux.HandleFunc("GET /sw.js", web.ServiceWorker(clientID))
	mux.HandleFunc("GET /guide/protein", web.ProteinGuide)
	mux.Handle("GET /static/", web.Static())
	return mux
}
