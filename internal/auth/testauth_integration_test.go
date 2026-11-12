package auth_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

func login(t *testing.T, url, cred string) int {
	t.Helper()
	resp, err := http.Post(url+"/auth/google", "application/json", bytes.NewReader([]byte(`{"credential":"`+cred+`"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Without the TestVerifier wrapper (production wiring), a test credential is just an invalid token.
func TestTestCredentialRejectedWithoutTestAuth(t *testing.T) {
	db := testutil.DB(t)
	mux := http.NewServeMux()
	(&auth.Handler{DB: db, Verifier: testutil.NewFakeVerifier()}).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if code := login(t, srv.URL, "test:synthetic-999998"); code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", code)
	}
}

func TestTestCredentialSignsInSyntheticUser(t *testing.T) {
	db := testutil.DB(t)
	const sub = "synthetic-999999" // outside the generator's %05d range
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM users WHERE google_sub = $1`, sub) })
	mux := http.NewServeMux()
	(&auth.Handler{DB: db, Verifier: auth.TestVerifier{Next: testutil.NewFakeVerifier()}}).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if code := login(t, srv.URL, "test:"+sub); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if code := login(t, srv.URL, "test:not-synthetic"); code != http.StatusUnauthorized {
		t.Fatalf("non-synthetic sub: status %d, want 401", code)
	}
}
