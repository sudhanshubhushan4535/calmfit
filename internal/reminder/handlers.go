package reminder

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
)

const maxBody = 4 << 10

// Handler serves the subscription endpoints. They exist only when a VAPID public key is configured.
type Handler struct {
	DB        *pgxpool.Pool
	PublicKey string
}

// Register adds GET /push/key, POST /push/subscribe and DELETE /push/subscribe.
func (h *Handler) Register(mux *http.ServeMux, requireUser func(http.Handler) http.Handler) {
	mux.Handle("GET /push/key", requireUser(http.HandlerFunc(h.key)))
	mux.Handle("POST /push/subscribe", requireUser(http.HandlerFunc(h.subscribe)))
	mux.Handle("DELETE /push/subscribe", requireUser(http.HandlerFunc(h.unsubscribe)))
}

func (h *Handler) key(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"public_key": h.PublicKey})
}

// subscription is PushSubscription.toJSON() from the browser.
type subscription struct {
	Endpoint       string   `json:"endpoint"`
	ExpirationTime *float64 `json:"expirationTime"` // ignored
	Keys           struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// subscribe upserts by endpoint: an endpoint is one browser, so the user who subscribed last owns it.
func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	var s subscription
	if !httpx.DecodeJSON(w, r, maxBody, &s) {
		return
	}
	if msg := validate(s); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	_, err := h.DB.Exec(r.Context(), `
		INSERT INTO push_subscriptions (endpoint, user_id, p256dh, auth) VALUES ($1, $2::uuid, $3, $4)
		ON CONFLICT (endpoint) DO UPDATE
		SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, created_at = now()`,
		s.Endpoint, uid, s.Keys.P256dh, s.Keys.Auth)
	if err != nil {
		httpx.InternalError(w, "reminder: subscribe", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unsubscribe deletes only the caller's own row; it is idempotent and says nothing about other users.
func (h *Handler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if !httpx.DecodeJSON(w, r, maxBody, &body) {
		return
	}
	if _, err := h.DB.Exec(r.Context(), `DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2::uuid`,
		body.Endpoint, uid); err != nil {
		httpx.InternalError(w, "reminder: unsubscribe", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pushHosts are the browser push services. The server POSTs to the endpoint a user sends, so only
// these hosts are accepted; anything else could point the sender at an internal address (SSRF).
// A leading dot matches any subdomain.
var pushHosts = []string{
	"fcm.googleapis.com",         // Chrome, Edge on Android, most Android browsers
	".push.services.mozilla.com", // Firefox
	".push.apple.com",            // Safari, iPhone home-screen apps
	".notify.windows.com",        // Edge on Windows
}

// AllowedEndpoint reports whether u is an https URL on a known push service, on the default port.
func AllowedEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			return true
		}
	}
	return false
}

// validate returns an error message, or "" when the subscription is acceptable.
func validate(s subscription) string {
	if len(s.Endpoint) > 1024 || !AllowedEndpoint(s.Endpoint) {
		return "endpoint must be an https URL of a browser push service"
	}
	if k := decodeB64(s.Keys.P256dh); len(k) != 65 || k[0] != 0x04 {
		return "keys.p256dh must be an uncompressed P-256 public key (base64url)"
	}
	if k := decodeB64(s.Keys.Auth); len(k) != 16 {
		return "keys.auth must be 16 bytes (base64url)"
	}
	return ""
}

// decodeB64 accepts base64url with or without padding (browsers send it unpadded).
func decodeB64(s string) []byte {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil
	}
	return b
}
