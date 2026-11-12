package auth

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// TestCredentialPrefix marks a load-test credential: "test:synthetic-00042".
const TestCredentialPrefix = "test:"

// syntheticSub is the only kind of account a test credential can sign into: the users created by
// `make seed-synthetic`. A real account's sub (Google's numeric id) never matches.
var syntheticSub = regexp.MustCompile(`^synthetic-\d{1,6}$`)

// TestVerifier lets load tests sign in synthetic users without Google (dev only, D39). It accepts
// "test:<sub>" for synthetic subs and hands every other credential to Next unchanged, so login
// still runs the real upsert + session path. cmd/api installs it only when TEST_AUTH=1 and
// APP_ENV=dev, and refuses to start with TEST_AUTH set in production.
type TestVerifier struct {
	Next Verifier
}

// Verify implements Verifier.
func (t TestVerifier) Verify(ctx context.Context, credential string) (Identity, error) {
	sub, ok := strings.CutPrefix(credential, TestCredentialPrefix)
	if !ok {
		return t.Next.Verify(ctx, credential)
	}
	if !syntheticSub.MatchString(sub) {
		return Identity{}, fmt.Errorf("%w: test credentials only sign in synthetic-N users", ErrInvalidToken)
	}
	return Identity{Sub: sub, Email: sub + "@synthetic.invalid", Name: "Synthetic " + strings.TrimPrefix(sub, "synthetic-"), EmailVerified: true}, nil
}
