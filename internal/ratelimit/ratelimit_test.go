package ratelimit_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/ratelimit"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

func istConfig(t *testing.T) config.Config {
	t.Helper()
	c, err := config.Parse([]byte(`{"timezone":"Asia/Kolkata","plate":{"protein_points":2,"fiber_points":1,"good_day_min":6}}`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func TestDailyLimit(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	ctx := context.Background()
	d := ratelimit.NewDaily(rdb, prefix, 30, istConfig(t))

	for i := 1; i <= 30; i++ {
		if err := d.Allow(ctx, "user-a"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if err := d.Allow(ctx, "user-a"); !errors.Is(err, ratelimit.ErrLimited) {
		t.Fatalf("call 31: err = %v, want ErrLimited", err)
	}
	// Still refused afterwards, and other users are unaffected.
	if err := d.Allow(ctx, "user-a"); !errors.Is(err, ratelimit.ErrLimited) {
		t.Errorf("call 32: err = %v, want ErrLimited", err)
	}
	if err := d.Allow(ctx, "user-b"); err != nil {
		t.Errorf("other user: %v", err)
	}
}

// Concurrent calls must not let more than the limit through (INCR is atomic).
func TestDailyLimitConcurrent(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	d := ratelimit.NewDaily(rdb, prefix, 30, istConfig(t))

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed, limited := 0, 0
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := d.Allow(context.Background(), "user-c")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				allowed++
			case errors.Is(err, ratelimit.ErrLimited):
				limited++
			default:
				t.Errorf("Allow: %v", err)
			}
		}()
	}
	wg.Wait()
	if allowed != 30 || limited != 20 {
		t.Errorf("allowed %d, limited %d; want 30 and 20", allowed, limited)
	}
}

func TestDailyResetsAtLocalMidnight(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	ctx := context.Background()
	cfg := istConfig(t)
	clk := &clock{}
	d := ratelimit.NewDaily(rdb, prefix, 2, cfg)
	ratelimit.SetNow(d, clk.now)

	// 23:59 IST on Mon 16 Nov: use up the day.
	clk.set(time.Date(2026, 11, 16, 23, 59, 0, 0, cfg.Location))
	for i := range 2 {
		if err := d.Allow(ctx, "u"); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if err := d.Allow(ctx, "u"); !errors.Is(err, ratelimit.ErrLimited) {
		t.Fatalf("err = %v, want ErrLimited", err)
	}
	// 00:01 IST on Tue 17 Nov (still 16 Nov in UTC): a new local day, a new allowance.
	clk.set(time.Date(2026, 11, 17, 0, 1, 0, 0, cfg.Location))
	if err := d.Allow(ctx, "u"); err != nil {
		t.Fatalf("after local midnight: %v", err)
	}
}

func TestDailyKeyExpiresAtLocalMidnight(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	ctx := context.Background()
	cfg := istConfig(t)
	clk := &clock{}
	d := ratelimit.NewDaily(rdb, prefix, 30, cfg)
	ratelimit.SetNow(d, clk.now)

	// 23:00 IST: the key must live exactly until local midnight, one hour away.
	clk.set(time.Date(2026, 11, 16, 23, 0, 0, 0, cfg.Location))
	if err := d.Allow(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("%s:u:2026-11-16", prefix)
	ttl, err := rdb.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 59*time.Minute || ttl > time.Hour {
		t.Errorf("TTL of %s = %v, want just under 1h", key, ttl)
	}
}

func TestDailyFailsClosedWhenRedisIsDown(t *testing.T) {
	// Nothing listens on port 1: every call must fail fast with ErrUnavailable, never allow.
	rdb, err := ratelimit.NewClient("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	d := ratelimit.NewDaily(rdb, "unused", 30, istConfig(t))

	start := time.Now()
	err = d.Allow(context.Background(), "u")
	if !errors.Is(err, ratelimit.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %v; an outage must fail fast", took)
	}
}
