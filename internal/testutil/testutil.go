// Package testutil holds helpers for DB-backed integration tests.
package testutil

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
)

const hint = "run `make test-db`, then `make test` (or `go test -short ./...` to skip DB tests)"

// DB connects to TEST_DATABASE_URL. It fails, never skips, when the DB isn't configured or seeded,
// so a missing env var can't masquerade as a passing run. Only `go test -short` skips.
func DB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping DB-backed test in -short mode")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatalf("TEST_DATABASE_URL is not set: %s", hint)
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v (%s)", err, hint)
	}
	t.Cleanup(db.Close)

	var seeded bool
	if err := db.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM program_templates WHERE id = 'ppl')`).Scan(&seeded); err != nil {
		t.Fatalf("test DB not migrated: %v (%s)", err, hint)
	}
	if !seeded {
		t.Fatalf("test DB has no 'ppl' template: %s", hint)
	}
	return db
}

// SubPrefix returns a unique google_sub prefix and deletes those users (cascading to sessions and
// user_programs) when the test ends.
func SubPrefix(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	prefix := "test-" + hex.EncodeToString(b) + "-"
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), `DELETE FROM users WHERE google_sub LIKE $1`, prefix+"%"); err != nil {
			t.Errorf("cleanup users %s*: %v", prefix, err)
		}
	})
	return prefix
}

// FakeVerifier accepts only credentials registered with Set.
type FakeVerifier struct {
	mu  sync.Mutex
	ids map[string]auth.Identity
}

// NewFakeVerifier returns an empty FakeVerifier.
func NewFakeVerifier() *FakeVerifier { return &FakeVerifier{ids: map[string]auth.Identity{}} }

// Set registers cred as a valid credential for id.
func (f *FakeVerifier) Set(cred string, id auth.Identity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids[cred] = id
}

// Verify implements auth.Verifier.
func (f *FakeVerifier) Verify(_ context.Context, cred string) (auth.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.ids[cred]
	if !ok {
		return auth.Identity{}, auth.ErrInvalidToken
	}
	return id, nil
}

// Login signs in a verified user with the given sub and returns a client holding its session cookie.
func Login(t *testing.T, baseURL string, fv *FakeVerifier, sub string) *http.Client {
	t.Helper()
	cred := "cred-" + sub
	fv.Set(cred, auth.Identity{Sub: sub, Email: sub + "@example.com", Name: "User " + sub, EmailVerified: true})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"credential": cred})
	resp, err := c.Post(baseURL+"/auth/google", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d", sub, resp.StatusCode)
	}
	return c
}

// Config loads the repo's config/app.json (found by walking up to go.mod), so tests use the same
// weights and timezone as the app.
func Config(t *testing.T) config.Config {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
	c, err := config.Load(filepath.Join(dir, "config", "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
