// Package auth implements Google sign-in, cookie sessions and the auth middleware.
package auth

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/api/idtoken"
)

// Identity is what we trust from a verified Google ID token.
type Identity struct {
	Sub           string
	Email         string
	Name          string
	EmailVerified bool
}

// Verifier turns a Google credential (ID token) into an Identity; tests swap in a fake.
type Verifier interface {
	Verify(ctx context.Context, credential string) (Identity, error)
}

// ErrInvalidToken wraps every verification failure.
var ErrInvalidToken = errors.New("invalid id token")

// GoogleVerifier checks tokens against Google's public keys.
type GoogleVerifier struct {
	ClientID string
}

// Verify validates signature, audience and expiry (via idtoken), then the issuer.
func (g GoogleVerifier) Verify(ctx context.Context, credential string) (Identity, error) {
	p, err := idtoken.Validate(ctx, credential, g.ClientID)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if p.Issuer != "accounts.google.com" && p.Issuer != "https://accounts.google.com" {
		return Identity{}, fmt.Errorf("%w: unexpected issuer %q", ErrInvalidToken, p.Issuer)
	}
	email, _ := p.Claims["email"].(string)
	verified, _ := p.Claims["email_verified"].(bool)
	name, _ := p.Claims["name"].(string)
	return Identity{Sub: p.Subject, Email: email, Name: name, EmailVerified: verified}, nil
}
