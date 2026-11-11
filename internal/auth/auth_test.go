package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

// fakeVerifier accepts only the credentials registered in ids.
type fakeVerifier struct {
	mu  sync.Mutex
	ids map[string]auth.Identity
}

func (f *fakeVerifier) set(cred string, id auth.Identity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids[cred] = id
}

func (f *fakeVerifier) Verify(_ context.Context, cred string) (auth.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.ids[cred]
	if !ok {
		return auth.Identity{}, auth.ErrInvalidToken
	}
	return id, nil
}

type env struct {
	srv    *httptest.Server
	db     *pgxpool.Pool
	fv     *fakeVerifier
	prefix string // google_sub prefix owned by this test; cleaned up afterwards
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.DB(t) // fails (never skips) if TEST_DATABASE_URL is unset; skips only with -short

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	prefix := "test-" + hex.EncodeToString(b) + "-"
	t.Cleanup(func() {
		// Sessions go with the users via ON DELETE CASCADE.
		if _, err := db.Exec(context.Background(), `DELETE FROM users WHERE google_sub LIKE $1`, prefix+"%"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	fv := &fakeVerifier{ids: map[string]auth.Identity{}}
	mux := http.NewServeMux()
	(&auth.Handler{DB: db, Verifier: fv}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{srv: srv, db: db, fv: fv, prefix: prefix}
}

func newClient(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func (e *env) login(t *testing.T, c *http.Client, cred string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"credential": cred})
	resp, err := c.Post(e.srv.URL+"/auth/google", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (e *env) do(t *testing.T, c *http.Client, method, path string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (e *env) identity(name string) auth.Identity {
	return auth.Identity{Sub: e.prefix + name, Email: name + "@example.com", Name: "User " + name, EmailVerified: true}
}

func sessionCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	return nil
}

func wantStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		t.Fatalf("%s %s: status %d, want %d (body %s)", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, strings.TrimSpace(b.String()))
	}
}

func decodeUser(t *testing.T, resp *http.Response) auth.User {
	t.Helper()
	var u auth.User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	return u
}

func TestLoginCreatesUserOnceAndUpdatesProfile(t *testing.T) {
	e := setup(t)
	id := e.identity("a")
	e.fv.set("tok-a1", id)

	first := e.login(t, newClient(t), "tok-a1")
	wantStatus(t, first, http.StatusOK)
	u1 := decodeUser(t, first)

	id.Email, id.Name = "a-new@example.com", "User A Renamed"
	e.fv.set("tok-a2", id)
	second := e.login(t, newClient(t), "tok-a2")
	wantStatus(t, second, http.StatusOK)
	u2 := decodeUser(t, second)

	if u1.ID != u2.ID {
		t.Fatalf("second login got a different user id: %s vs %s", u1.ID, u2.ID)
	}
	var n int
	var email, name string
	if err := e.db.QueryRow(context.Background(),
		`SELECT count(*) OVER (), email, name FROM users WHERE google_sub = $1`, id.Sub).Scan(&n, &email, &name); err != nil {
		t.Fatal(err)
	}
	if n != 1 || email != "a-new@example.com" || name != "User A Renamed" {
		t.Fatalf("got rows=%d email=%q name=%q; want 1 row with updated profile", n, email, name)
	}
}

func TestInvalidTokenIs401(t *testing.T) {
	e := setup(t)
	resp := e.login(t, newClient(t), "not-a-real-token")
	wantStatus(t, resp, http.StatusUnauthorized)
	if c := sessionCookie(t, resp); c != nil {
		t.Fatalf("invalid token set a session cookie: %v", c)
	}
}

func TestUnverifiedEmailIs403(t *testing.T) {
	e := setup(t)
	id := e.identity("unverified")
	id.EmailVerified = false
	e.fv.set("tok-u", id)
	resp := e.login(t, newClient(t), "tok-u")
	wantStatus(t, resp, http.StatusForbidden)
	var n int
	_ = e.db.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE google_sub = $1`, id.Sub).Scan(&n)
	if n != 0 {
		t.Fatalf("unverified login created %d user rows", n)
	}
}

func TestMe(t *testing.T) {
	e := setup(t)
	c := newClient(t)
	wantStatus(t, e.do(t, c, "GET", "/me", nil), http.StatusUnauthorized)

	id := e.identity("me")
	e.fv.set("tok-me", id)
	wantStatus(t, e.login(t, c, "tok-me"), http.StatusOK)

	resp := e.do(t, c, "GET", "/me", nil) // cookie comes from the jar
	wantStatus(t, resp, http.StatusOK)
	u := decodeUser(t, resp)
	if u.ID == "" || u.Email != id.Email || u.Name == nil || *u.Name != id.Name {
		t.Fatalf("/me = %+v, want email %q name %q", u, id.Email, id.Name)
	}
}

func TestExpiredSessionIs401(t *testing.T) {
	e := setup(t)
	c := newClient(t)
	id := e.identity("exp")
	e.fv.set("tok-exp", id)
	wantStatus(t, e.login(t, c, "tok-exp"), http.StatusOK)
	wantStatus(t, e.do(t, c, "GET", "/me", nil), http.StatusOK)

	if _, err := e.db.Exec(context.Background(), `
		UPDATE sessions s SET expires_at = now() - interval '1 second'
		FROM users u WHERE s.user_id = u.id AND u.google_sub = $1`, id.Sub); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.do(t, c, "GET", "/me", nil), http.StatusUnauthorized)
}

func TestLogoutInvalidatesSession(t *testing.T) {
	e := setup(t)
	c := newClient(t)
	id := e.identity("out")
	e.fv.set("tok-out", id)
	login := e.login(t, c, "tok-out")
	wantStatus(t, login, http.StatusOK)
	old := sessionCookie(t, login)

	logout := e.do(t, c, "POST", "/auth/logout", nil)
	wantStatus(t, logout, http.StatusNoContent)
	if cl := sessionCookie(t, logout); cl == nil || cl.MaxAge >= 0 {
		t.Fatalf("logout should clear the cookie, got %v", cl)
	}

	// Replaying the old cookie must fail even though the client still holds it.
	wantStatus(t, e.do(t, &http.Client{}, "GET", "/me", old), http.StatusUnauthorized)

	var n int
	_ = e.db.QueryRow(context.Background(), `
		SELECT count(*) FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.google_sub = $1`, id.Sub).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sessions remain after logout", n)
	}
	// Logging out again is harmless.
	wantStatus(t, e.do(t, &http.Client{}, "POST", "/auth/logout", old), http.StatusNoContent)
}

func TestDeletedUserCannotSignInAndRowIsUntouched(t *testing.T) {
	e := setup(t)
	id := e.identity("del")
	e.fv.set("tok-del", id)
	wantStatus(t, e.login(t, newClient(t), "tok-del"), http.StatusOK)

	// Simulate account deletion: anonymise, soft-delete, revoke sessions.
	ctx := context.Background()
	if _, err := e.db.Exec(ctx, `UPDATE users SET deleted_at = now(), email = 'anon', name = 'anon' WHERE google_sub = $1`, id.Sub); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(ctx, `DELETE FROM sessions s USING users u WHERE s.user_id = u.id AND u.google_sub = $1`, id.Sub); err != nil {
		t.Fatal(err)
	}

	id.Email, id.Name = "del-again@example.com", "Back Again"
	e.fv.set("tok-del2", id)
	resp := e.login(t, newClient(t), "tok-del2")
	wantStatus(t, resp, http.StatusForbidden)
	if c := sessionCookie(t, resp); c != nil {
		t.Fatalf("deleted user got a session cookie: %v", c)
	}

	var email, name string
	var sessions int
	if err := e.db.QueryRow(ctx, `
		SELECT u.email, u.name, (SELECT count(*) FROM sessions s WHERE s.user_id = u.id)
		FROM users u WHERE u.google_sub = $1`, id.Sub).Scan(&email, &name, &sessions); err != nil {
		t.Fatal(err)
	}
	if email != "anon" || name != "anon" || sessions != 0 {
		t.Fatalf("deleted row changed: email=%q name=%q sessions=%d", email, name, sessions)
	}
}

func TestOnlyTokenHashIsStoredAndCookieFlags(t *testing.T) {
	e := setup(t)
	id := e.identity("hash")
	e.fv.set("tok-hash", id)
	resp := e.login(t, newClient(t), "tok-hash")
	wantStatus(t, resp, http.StatusOK)

	c := sessionCookie(t, resp)
	if c == nil {
		t.Fatal("no session cookie")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure || c.Path != "/" {
		t.Fatalf("cookie flags: HttpOnly=%v SameSite=%v Secure=%v Path=%q", c.HttpOnly, c.SameSite, c.Secure, c.Path)
	}
	if want := int((30 * 24 * time.Hour).Seconds()); c.MaxAge != want {
		t.Fatalf("cookie MaxAge = %d, want %d", c.MaxAge, want)
	}

	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(raw) != 32 {
		t.Fatalf("cookie is not a 32-byte base64url token: %v (len %d)", err, len(raw))
	}
	sum := sha256.Sum256(raw)

	var hashed, plain int
	var expiresIn time.Duration
	ctx := context.Background()
	if err := e.db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id_hash = $1`, sum[:]).Scan(&hashed); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id_hash = $1 OR id_hash = $2`, raw, []byte(c.Value)).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(ctx, `SELECT expires_at - now() FROM sessions WHERE id_hash = $1`, sum[:]).Scan(&expiresIn); err != nil {
		t.Fatal(err)
	}
	if hashed != 1 || plain != 0 {
		t.Fatalf("want exactly one row keyed by sha256(token) and none by the raw token; got hashed=%d raw=%d", hashed, plain)
	}
	if d := 30*24*time.Hour - expiresIn; d < 0 || d > time.Minute {
		t.Fatalf("session expires in %v, want ~30 days", expiresIn)
	}
}

func TestLoginRequiresJSON(t *testing.T) {
	e := setup(t)
	resp, err := http.Post(e.srv.URL+"/auth/google", "application/x-www-form-urlencoded", strings.NewReader("credential=x"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	wantStatus(t, resp, http.StatusUnsupportedMediaType)
}
