package reminder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

// Subscription is one stored browser subscription.
type Subscription struct {
	Endpoint, P256dh, Auth string
}

// Sender delivers one encrypted push message and returns the push service's HTTP status.
type Sender interface {
	Send(ctx context.Context, s Subscription, payload []byte) (int, error)
}

// WebPush sends with VAPID (RFC 8292) and aes128gcm payload encryption (RFC 8291).
type WebPush struct {
	PublicKey, PrivateKey, Subject string
	Client                         *http.Client
}

// ttl: a reminder older than 12 hours is useless, so the push service may drop it.
const ttl = 12 * 60 * 60

// Send implements Sender.
func (w WebPush) Send(ctx context.Context, s Subscription, payload []byte) (int, error) {
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := webpush.SendNotificationWithContext(ctx, payload,
		&webpush.Subscription{Endpoint: s.Endpoint, Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}},
		&webpush.Options{HTTPClient: client, Subscriber: w.Subject, VAPIDPublicKey: w.PublicKey,
			VAPIDPrivateKey: w.PrivateKey, TTL: ttl, Urgency: webpush.UrgencyNormal})
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
	return resp.StatusCode, nil
}

// Payload is what the service worker shows.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

// Message is one reminder that was (or, in a dry run, would be) sent.
type Message struct {
	UserID string
	Text   string
}

// Result counts one run.
type Result struct {
	Candidates int // onboarded, subscribed, not yet reminded today
	Due        int
	Sent       int // push service accepted (2xx)
	Gone       int // 404/410: subscription deleted
	Failed     int // other errors; not retried today
	Messages   []Message
	Errors     []string // why sends failed, at most maxErrors (for the cron log)
}

const maxErrors = 10

func (r *Result) fail(msg string) {
	r.Failed++
	if len(r.Errors) < maxErrors {
		r.Errors = append(r.Errors, msg)
	}
}

type candidate struct {
	uid          string
	daysPerWeek  int
	lastDone     *time.Time
	startedToday bool
}

// Options narrow a run.
type Options struct {
	DryRun bool     // decide and build the texts, but send and record nothing
	Users  []string // only these user ids (a manual test send, or a test); nil = everyone
}

// Run sends today's reminders; now decides the local date in loc. The reminder_log row is written
// before sending, so a reminder goes out at most once per user per day even if two runs overlap;
// a failed send is not retried that day.
func Run(ctx context.Context, db *pgxpool.Pool, s Sender, loc *time.Location, now time.Time, o Options) (Result, error) {
	var res Result
	y, m, d := now.In(loc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, loc)
	cands, err := candidates(ctx, db, loc.String(), today, o.Users)
	if err != nil {
		return res, err
	}
	res.Candidates = len(cands)
	for _, c := range cands {
		if !Due(today, c.lastDone, c.daysPerWeek, c.startedToday) {
			continue
		}
		res.Due++
		p, ok, err := workout.Next(ctx, db, c.uid)
		if err != nil {
			return res, fmt.Errorf("next workout for %s: %w", c.uid, err)
		}
		if !ok {
			continue
		}
		msg := Message{UserID: c.uid, Text: Text(p)}
		res.Messages = append(res.Messages, msg)
		if o.DryRun {
			continue
		}
		tag, err := db.Exec(ctx, `INSERT INTO reminder_log (user_id, local_date) VALUES ($1::uuid, $2) ON CONFLICT DO NOTHING`,
			c.uid, today.Format(time.DateOnly))
		if err != nil {
			return res, fmt.Errorf("reminder log: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue // another run already reminded this user today
		}
		if err := sendAll(ctx, db, s, c.uid, msg.Text, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

func sendAll(ctx context.Context, db *pgxpool.Pool, s Sender, uid, text string, res *Result) error {
	payload, _ := json.Marshal(Payload{Title: "CalmFit", Body: text, URL: "/app"})
	rows, err := db.Query(ctx, `SELECT endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = $1::uuid ORDER BY created_at`, uid)
	if err != nil {
		return fmt.Errorf("load subscriptions: %w", err)
	}
	var subs []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.Endpoint, &sub.P256dh, &sub.Auth); err != nil {
			rows.Close()
			return fmt.Errorf("scan subscription: %w", err)
		}
		subs = append(subs, sub)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load subscriptions: %w", err)
	}
	for _, sub := range subs {
		status, err := s.Send(ctx, sub, payload)
		host := sub.Endpoint
		if u, perr := url.Parse(sub.Endpoint); perr == nil {
			host = u.Host // never log the full endpoint: it is a bearer capability for that browser
		}
		switch {
		case err != nil:
			res.fail(fmt.Sprintf("%s: %v", host, err))
		case status == http.StatusNotFound || status == http.StatusGone:
			// The browser unsubscribed or the subscription expired: never send to it again.
			if _, err := db.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2::uuid`, sub.Endpoint, uid); err != nil {
				return fmt.Errorf("delete gone subscription: %w", err)
			}
			res.Gone++
		case status >= 200 && status < 300:
			res.Sent++
		default:
			res.fail(fmt.Sprintf("%s: HTTP %d", host, status))
		}
	}
	return nil
}

// candidates are onboarded, active users with a subscription and no reminder today, with their
// last completed workout's local date and whether a workout was started today.
func candidates(ctx context.Context, db *pgxpool.Pool, tz string, today time.Time, users []string) ([]candidate, error) {
	rows, err := db.Query(ctx, `
		SELECT u.id::text, up.days_per_week,
		       (SELECT max((w.completed_at AT TIME ZONE $1)::date) FROM workouts w
		         WHERE w.user_id = u.id AND w.status = 'completed'),
		       EXISTS (SELECT 1 FROM workouts w WHERE w.user_id = u.id AND w.status = 'in_progress'
		                 AND (w.started_at AT TIME ZONE $1)::date = $2::date)
		FROM users u JOIN user_programs up ON up.user_id = u.id
		WHERE u.deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM push_subscriptions ps WHERE ps.user_id = u.id)
		  AND NOT EXISTS (SELECT 1 FROM reminder_log rl WHERE rl.user_id = u.id AND rl.local_date = $2::date)
		  AND ($3::uuid[] IS NULL OR u.id = ANY ($3::uuid[]))
		ORDER BY u.id`, tz, today.Format(time.DateOnly), users)
	if err != nil {
		return nil, fmt.Errorf("reminder candidates: %w", err)
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		var last *time.Time
		if err := rows.Scan(&c.uid, &c.daysPerWeek, &last, &c.startedToday); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		if last != nil {
			// A pgx date is midnight UTC; re-anchor it to midnight in the app's zone.
			l := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, today.Location())
			c.lastDone = &l
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
