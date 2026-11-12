// Command api serves the CalmFit HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/metrics"
	"github.com/sudhanshubhushan4535/calmfit/internal/ratelimit"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/voice"
)

const (
	addr = ":8080"
	// defaultMetricsAddr is a separate listener so /metrics never sits on the public port (D37).
	defaultMetricsAddr = ":9101"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dsn, err := requireEnv("DATABASE_URL")
	if err != nil {
		return err
	}
	clientID, err := requireEnv("GOOGLE_CLIENT_ID")
	if err != nil {
		return err
	}

	cfgPath := os.Getenv("APP_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/app.json"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	// Voice is optional. When it's on, a missing key or Redis URL fails here, not on the first request.
	var opts []server.Option
	if cfg.Voice.Enabled {
		key, err := requireEnv("ANTHROPIC_API_KEY")
		if err != nil {
			return fmt.Errorf("voice.enabled is true in %s: %w", cfgPath, err)
		}
		redisURL, err := requireEnv("REDIS_URL")
		if err != nil {
			return fmt.Errorf("voice.enabled is true in %s: %w", cfgPath, err)
		}
		rdb, err := ratelimit.NewClient(redisURL)
		if err != nil {
			return err
		}
		defer rdb.Close()
		// No startup ping: the limiter fails closed (503) while Redis is down, and the rest of the API
		// doesn't need Redis at all.
		limiter := ratelimit.NewDaily(rdb, "voice", cfg.Voice.DailyLimit, cfg)
		opts = append(opts, server.WithVoice(voice.NewAPIParser(key, cfg.Voice.Model), limiter))
		log.Printf("voice logging on (%s, %d parses per user per day)", cfg.Voice.Model, cfg.Voice.DailyLimit)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("db pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("db ping: %w", err)
	}

	reg := metrics.NewRegistry()
	httpMetrics := metrics.NewHTTP(reg)
	reg.MustRegister(metrics.NewOutboxCollector(pool))
	if _, err := metrics.Start(ctx, envOr("METRICS_ADDR", defaultMetricsAddr), reg); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpMetrics.Wrap(server.New(pool, auth.GoogleVerifier{ClientID: clientID}, clientID, cfg, opts...)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Printf("listening on http://localhost%s", addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	log.Println("shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Println("stopped")
	return nil
}

func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("%s is not set (see .env.example)", key)
	}
	return v, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
