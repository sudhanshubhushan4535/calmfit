// Package ratelimit holds the per-user daily limits kept in Redis (Redis is for rate limiting
// only; Postgres stays the source of truth). It fails closed: if Redis can't answer, the call is
// refused, so the limit can never silently disappear (D34).
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
)

var (
	// ErrLimited means the user has used today's allowance.
	ErrLimited = errors.New("ratelimit: daily limit reached")
	// ErrUnavailable means Redis couldn't be asked; the caller must refuse (fail closed).
	ErrUnavailable = errors.New("ratelimit: limiter unavailable")
)

// opTimeout bounds one Redis round trip, so a hung Redis fails fast instead of holding a request.
const opTimeout = 500 * time.Millisecond

// NewClient connects to url (redis://host:port/db) with short timeouts and no retries, so an
// outage turns into ErrUnavailable within opTimeout.
func NewClient(url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: redis url: %w", err)
	}
	opt.DialTimeout, opt.ReadTimeout, opt.WriteTimeout = opTimeout, opTimeout, opTimeout
	opt.MaxRetries = -1 // go-redis: -1 disables retries
	return redis.NewClient(opt), nil
}

// Daily allows Limit calls per user per local calendar day (the app's config timezone, D4).
type Daily struct {
	rdb    *redis.Client
	prefix string // key namespace, e.g. "voice"
	limit  int
	cfg    config.Config
	now    func() time.Time
}

// NewDaily returns a limiter whose keys are "<prefix>:<user>:<local date>".
func NewDaily(rdb *redis.Client, prefix string, limit int, cfg config.Config) *Daily {
	return &Daily{rdb: rdb, prefix: prefix, limit: limit, cfg: cfg, now: time.Now}
}

// Allow counts one call for userID and reports whether it is within today's limit. Refused calls
// are counted too; that is harmless, because the count only matters until local midnight.
func (d *Daily) Allow(ctx context.Context, userID string) error {
	now := d.now()
	today := d.cfg.Today(now)
	key := fmt.Sprintf("%s:%s:%s", d.prefix, userID, today.Format(time.DateOnly))
	// A relative expiry (midnight minus now) is immune to clock skew between the API and Redis.
	ttl := d.cfg.StartOf(today.AddDate(0, 0, 1)).Sub(now)

	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	var incr *redis.IntCmd
	_, err := d.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		incr = p.Incr(ctx, key)
		p.PExpire(ctx, key, ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if incr.Val() > int64(d.limit) {
		return ErrLimited
	}
	return nil
}
