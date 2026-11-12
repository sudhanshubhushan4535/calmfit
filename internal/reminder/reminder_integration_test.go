package reminder_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/reminder"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

type env struct {
	db     *pgxpool.Pool
	url    string
	fv     *testutil.FakeVerifier
	prefix string
}

func setup(t *testing.T, opts ...server.Option) *env {
	t.Helper()
	db := testutil.DB(t)
	prefix := testutil.SubPrefix(t, db)
	fv := testutil.NewFakeVerifier()
	srv := httptest.NewServer(server.New(db, fv, "test-client-id", testutil.Config(t), opts...))
	t.Cleanup(srv.Close)
	return &env{db: db, url: srv.URL, fv: fv, prefix: prefix}
}

func (e *env) uid(t *testing.T, sub string) string {
	t.Helper()
	var id string
	if err := e.db.QueryRow(context.Background(), `SELECT id::text FROM users WHERE google_sub = $1`, e.prefix+sub).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func send(t *testing.T, c *http.Client, method, url, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// browserKeys is what a browser's PushManager generates: a P-256 key pair and a 16-byte secret.
type browserKeys struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newBrowserKeys(t *testing.T) browserKeys {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return browserKeys{priv, auth}
}

func (k browserKeys) json(endpoint string) string {
	b, _ := json.Marshal(map[string]any{"endpoint": endpoint, "expirationTime": nil, "keys": map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(k.priv.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString(k.auth),
	}})
	return string(b)
}

// decrypt opens an aes128gcm Web Push body (RFC 8291 + RFC 8188, single record).
func (k browserKeys) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	salt, idLen := body[:16], int(body[20])
	asPub, ct := body[21:21+idLen], body[21+idLen:]
	_ = binary.BigEndian.Uint32(body[16:20]) // record size
	peer, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := k.priv.ECDH(peer)
	if err != nil {
		t.Fatal(err)
	}
	prkKey, _ := hkdf.Extract(sha256.New, shared, k.auth)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(k.priv.PublicKey().Bytes())+string(asPub), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatal("missing last-record delimiter")
	}
	return plain[:len(plain)-1]
}

// pushService is a fake browser push service; each path answers with its own status.
type pushService struct {
	*httptest.Server
	mu   sync.Mutex
	got  map[string][][]byte // path → bodies
	auth []string
}

func newPushService(t *testing.T, statuses map[string]int) *pushService {
	ps := &pushService{got: map[string][][]byte{}}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		ps.got[r.URL.Path] = append(ps.got[r.URL.Path], b)
		ps.auth = append(ps.auth, r.Header.Get("Authorization")+" | "+r.Header.Get("Content-Encoding")+" | "+r.Header.Get("TTL"))
		ps.mu.Unlock()
		code := statuses[r.URL.Path]
		if code == 0 {
			code = http.StatusCreated
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(ps.Close)
	return ps
}

// onboardAndComplete onboards a user and completes one push workout, then moves its completion
// back daysAgo days (in the app's zone it lands on that local date).
func (e *env) onboardAndComplete(t *testing.T, c *http.Client, uid string, daysPerWeek, daysAgo int) {
	t.Helper()
	testutil.Do(t, c, "POST", e.url+"/onboarding", `{"days_per_week":`+strconv.Itoa(daysPerWeek)+`}`)
	var w struct{ ID string }
	if err := json.Unmarshal(testutil.Do(t, c, "POST", e.url+"/workouts/start", ""), &w); err != nil {
		t.Fatal(err)
	}
	testutil.Do(t, c, "POST", e.url+"/workouts/"+w.ID+"/exercises/Side_Lateral_Raise/cold-start", `{"usual_kg":5}`)
	testutil.Do(t, c, "POST", e.url+"/workouts/"+w.ID+"/sets", `{"exercise_id":"Side_Lateral_Raise","set_number":1,"weight_g":5000,"reps":12}`)
	testutil.Do(t, c, "POST", e.url+"/workouts/"+w.ID+"/complete", "")
	if _, err := e.db.Exec(context.Background(), `
		UPDATE workouts SET started_at = started_at - make_interval(days => $2), completed_at = completed_at - make_interval(days => $2)
		WHERE id = $1::uuid`, w.ID, daysAgo); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeRoutes(t *testing.T) {
	// Without a VAPID key the routes don't exist.
	plain := setup(t)
	c0 := testutil.Login(t, plain.url, plain.fv, plain.prefix+"x")
	if code := send(t, c0, "GET", plain.url+"/push/key", ""); code != http.StatusNotFound {
		t.Fatalf("GET /push/key without VAPID = %d, want 404", code)
	}

	e := setup(t, server.WithPush("public-key-123"))
	a := testutil.Login(t, e.url, e.fv, e.prefix+"a")
	b := testutil.Login(t, e.url, e.fv, e.prefix+"b")
	uidA := e.uid(t, "a")
	ctx := context.Background()

	if code := send(t, &http.Client{}, "POST", e.url+"/push/subscribe", "{}"); code != http.StatusUnauthorized {
		t.Fatalf("subscribe without a session = %d", code)
	}
	var key struct {
		PublicKey string `json:"public_key"`
	}
	_ = json.Unmarshal(testutil.Do(t, a, "GET", e.url+"/push/key", ""), &key)
	if key.PublicKey != "public-key-123" {
		t.Fatalf("public key %q", key.PublicKey)
	}

	k := newBrowserKeys(t)
	endpoint := "https://fcm.googleapis.com/fcm/send/" + e.prefix + "a"
	for name, body := range map[string]string{
		"internal address": k.json("https://127.0.0.1/x"),
		"http":             k.json("http://fcm.googleapis.com/fcm/send/x"),
		"bad keys":         `{"endpoint":"` + endpoint + `","keys":{"p256dh":"AAAA","auth":"AAAA"}}`,
		"unknown field":    `{"endpoint":"` + endpoint + `","extra":1}`,
	} {
		if code := send(t, a, "POST", e.url+"/push/subscribe", body); code != http.StatusBadRequest {
			t.Errorf("%s: subscribe = %d, want 400", name, code)
		}
	}
	if code := send(t, a, "POST", e.url+"/push/subscribe", k.json(endpoint)); code != http.StatusNoContent {
		t.Fatalf("subscribe = %d", code)
	}
	owner := func() string {
		var uid string
		if err := e.db.QueryRow(ctx, `SELECT coalesce((SELECT user_id::text FROM push_subscriptions WHERE endpoint = $1), '')`, endpoint).Scan(&uid); err != nil {
			t.Fatal(err)
		}
		return uid
	}
	if owner() != uidA {
		t.Fatal("subscription not stored for A")
	}
	// B can't delete A's subscription; the call still answers 204 and reveals nothing.
	if code := send(t, b, "DELETE", e.url+"/push/subscribe", `{"endpoint":"`+endpoint+`"}`); code != http.StatusNoContent || owner() != uidA {
		t.Fatalf("B's delete: %d, owner %q", code, owner())
	}
	if code := send(t, a, "DELETE", e.url+"/push/subscribe", `{"endpoint":"`+endpoint+`"}`); code != http.StatusNoContent || owner() != "" {
		t.Fatalf("A's delete: %d, owner %q", code, owner())
	}
	// Same browser, next user to sign in and subscribe owns the endpoint.
	send(t, a, "POST", e.url+"/push/subscribe", k.json(endpoint))
	send(t, b, "POST", e.url+"/push/subscribe", k.json(endpoint))
	if owner() != e.uid(t, "b") {
		t.Fatal("re-subscribe from the same browser did not move the endpoint to B")
	}
}

func TestRunSendsOnceAndDropsGone(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cfg := testutil.Config(t)
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	ps := newPushService(t, map[string]int{"/gone": http.StatusGone})

	// due: 3/wk, last workout 2 days ago, two devices (one of them gone).
	// rested: 3/wk, last workout yesterday → not due. today: started a workout today → not due.
	users := map[string]*http.Client{}
	for _, s := range []string{"due", "rested", "today"} {
		users[s] = testutil.Login(t, e.url, e.fv, e.prefix+s)
	}
	uid := map[string]string{"due": e.uid(t, "due"), "rested": e.uid(t, "rested"), "today": e.uid(t, "today")}
	e.onboardAndComplete(t, users["due"], uid["due"], 3, 2)
	e.onboardAndComplete(t, users["rested"], uid["rested"], 3, 1)
	e.onboardAndComplete(t, users["today"], uid["today"], 3, 5)
	testutil.Do(t, users["today"], "POST", e.url+"/workouts/start", "")

	keys := map[string]browserKeys{}
	insert := func(user, path string) {
		k := newBrowserKeys(t)
		keys[path] = k
		if _, err := e.db.Exec(ctx, `INSERT INTO push_subscriptions (endpoint, user_id, p256dh, auth) VALUES ($1, $2::uuid, $3, $4)`,
			ps.URL+path, uid[user], base64.RawURLEncoding.EncodeToString(k.priv.PublicKey().Bytes()),
			base64.RawURLEncoding.EncodeToString(k.auth)); err != nil {
			t.Fatal(err)
		}
	}
	insert("due", "/phone")
	insert("due", "/gone")
	insert("rested", "/rested")
	insert("today", "/today")

	sender := reminder.WebPush{PublicKey: pub, PrivateKey: priv, Subject: "mailto:test@example.com"}
	only := reminder.Options{Users: []string{uid["due"], uid["rested"], uid["today"]}}
	now := time.Now()

	dry := only
	dry.DryRun = true
	res, err := reminder.Run(ctx, e.db, sender, cfg.Location, now, dry)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates != 3 || res.Due != 1 || res.Sent != 0 || len(ps.got) != 0 {
		t.Fatalf("dry run: %+v, push service got %d", res, len(ps.got))
	}

	res, err = reminder.Run(ctx, e.db, sender, cfg.Location, now, only)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates != 3 || res.Due != 1 || res.Sent != 1 || res.Gone != 1 || res.Failed != 0 {
		t.Fatalf("run: %+v", res)
	}
	// The text is exactly what the app's next-workout preview gives.
	p, _, _ := workout.Next(ctx, e.db, uid["due"])
	want := reminder.Text(p)
	if !strings.HasPrefix(want, "Pull day today.") {
		t.Fatalf("unexpected text %q", want)
	}
	if len(ps.got["/phone"]) != 1 {
		t.Fatalf("phone got %d messages", len(ps.got["/phone"]))
	}
	var payload reminder.Payload
	if err := json.Unmarshal(keys["/phone"].decrypt(t, ps.got["/phone"][0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload != (reminder.Payload{Title: "CalmFit", Body: want, URL: "/app"}) {
		t.Fatalf("payload %+v, want body %q", payload, want)
	}
	if a := ps.auth[0]; !strings.HasPrefix(a, "vapid t=") || !strings.Contains(a, "k="+pub) || !strings.Contains(a, "| aes128gcm | 43200") {
		t.Fatalf("headers: %s", a)
	}
	var left int
	_ = e.db.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions WHERE user_id = $1::uuid`, uid["due"]).Scan(&left)
	if left != 1 {
		t.Fatalf("gone subscription not deleted: %d left", left)
	}

	// A second run the same day sends nothing.
	res, err = reminder.Run(ctx, e.db, sender, cfg.Location, now, only)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates != 2 || res.Due != 0 || len(ps.got["/phone"]) != 1 {
		t.Fatalf("second run: %+v, phone got %d", res, len(ps.got["/phone"]))
	}
}
