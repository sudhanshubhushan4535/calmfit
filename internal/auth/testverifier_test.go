package auth

import (
	"context"
	"errors"
	"testing"
)

type nextVerifier struct{ got string }

func (n *nextVerifier) Verify(_ context.Context, cred string) (Identity, error) {
	n.got = cred
	return Identity{Sub: "google-sub", EmailVerified: true}, nil
}

func TestTestVerifier(t *testing.T) {
	next := &nextVerifier{}
	v := TestVerifier{Next: next}
	ctx := context.Background()

	id, err := v.Verify(ctx, "test:synthetic-00042")
	if err != nil || id.Sub != "synthetic-00042" || !id.EmailVerified || id.Email != "synthetic-00042@synthetic.invalid" {
		t.Fatalf("synthetic: %+v, %v", id, err)
	}
	if next.got != "" {
		t.Fatal("a test credential must not reach the Google verifier")
	}

	// Only synthetic accounts: never a real user's sub, never anything else.
	for _, cred := range []string{"test:", "test:115550000000000000001", "test:real-person", "test:synthetic-", "test:synthetic-1234567", "test:synthetic-1 ", "test:Synthetic-1"} {
		if _, err := v.Verify(ctx, cred); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%q: err %v, want ErrInvalidToken", cred, err)
		}
	}

	// Everything else goes to Next untouched.
	id, err = v.Verify(ctx, "eyJhbGciOi.google.token")
	if err != nil || id.Sub != "google-sub" || next.got != "eyJhbGciOi.google.token" {
		t.Fatalf("passthrough: %+v, %v, next got %q", id, err, next.got)
	}
}
