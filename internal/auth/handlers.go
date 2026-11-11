package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
)

const (
	// CookieName is the session cookie.
	CookieName         = "calmfit_session"
	sessionTTL         = 30 * 24 * time.Hour
	sessionTTLInterval = "30 days" // keep in sync with sessionTTL; expiry is computed by the DB clock
	tokenBytes         = 32
	maxBodyBytes       = 64 << 10
)

// Handler serves the auth endpoints.
type Handler struct {
	DB       *pgxpool.Pool
	Verifier Verifier
}

// Register adds the auth routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/google", h.googleLogin)
	mux.HandleFunc("POST /auth/logout", h.logout)
	mux.Handle("GET /me", h.RequireUser(http.HandlerFunc(h.me)))
}

type ctxKey struct{}

// UserID returns the authenticated user's id put there by RequireUser.
func UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKey{}).(string)
	return id, ok
}

// RequireUser rejects requests without a live session with 401.
func (h *Handler) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash, ok := cookieHash(r)
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		uid, err := sessionUserID(r.Context(), h.DB, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err != nil {
			httpx.InternalError(w, "auth: session lookup", err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, uid)))
	})
}

func (h *Handler) googleLogin(w http.ResponseWriter, r *http.Request) {
	// Requiring JSON forces a CORS preflight for cross-site callers, which blocks login CSRF.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		httpx.WriteError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	var body struct {
		Credential string `json:"credential"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body); err != nil || body.Credential == "" {
		httpx.WriteError(w, http.StatusBadRequest, "body must be {\"credential\": \"...\"}")
		return
	}

	id, err := h.Verifier.Verify(r.Context(), body.Credential)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid credential")
		return
	}
	if !id.EmailVerified {
		httpx.WriteError(w, http.StatusForbidden, "email not verified")
		return
	}

	uid, err := upsertUser(r.Context(), h.DB, id)
	if errors.Is(err, ErrUserDeleted) {
		httpx.WriteError(w, http.StatusForbidden, "account deleted")
		return
	}
	if err != nil {
		httpx.InternalError(w, "auth: upsert user", err)
		return
	}

	token := make([]byte, tokenBytes)
	if _, err := rand.Read(token); err != nil {
		httpx.InternalError(w, "auth: generate token", err)
		return
	}
	hash := sha256.Sum256(token)
	if err := createSession(r.Context(), h.DB, hash[:], uid); err != nil {
		httpx.InternalError(w, "auth: create session", err)
		return
	}
	http.SetCookie(w, sessionCookie(r, base64.RawURLEncoding.EncodeToString(token), int(sessionTTL.Seconds())))

	u, err := getUser(r.Context(), h.DB, uid)
	if err != nil {
		httpx.InternalError(w, "auth: load user", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, u)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	uid, _ := UserID(r.Context())
	u, err := getUser(r.Context(), h.DB, uid)
	if errors.Is(err, pgx.ErrNoRows) { // deleted between the session check and now
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		httpx.InternalError(w, "auth: load user", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, u)
}

// logout is idempotent: it deletes the session if there is one and always clears the cookie.
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if hash, ok := cookieHash(r); ok {
		if err := deleteSession(r.Context(), h.DB, hash); err != nil {
			httpx.InternalError(w, "auth: delete session", err)
			return
		}
	}
	http.SetCookie(w, sessionCookie(r, "", -1))
	w.WriteHeader(http.StatusNoContent)
}

// cookieHash decodes the session cookie and returns SHA-256 of the raw token.
func cookieHash(r *http.Request) ([]byte, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(raw) != tokenBytes {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

func sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isLocalhost(r.Host),
	}
}

// isLocalhost reports whether host (optionally with port) is a loopback name, where cookies can't be Secure over http.
func isLocalhost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
