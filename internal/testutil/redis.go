package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/sudhanshubhushan4535/calmfit/internal/ratelimit"
)

const redisHint = "run `make redis-up`, set TEST_REDIS_URL (see .env.example), then `make test` (or `go test -short ./...` to skip)"

// Redis connects to TEST_REDIS_URL and returns a client plus a key prefix unique to this test;
// keys under the prefix are deleted when the test ends. Like DB and KafkaTopics, it fails, never
// skips, when Redis isn't configured or reachable. Only `go test -short` skips.
func Redis(t *testing.T) (*redis.Client, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Redis-backed test in -short mode")
	}
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Fatalf("TEST_REDIS_URL is not set: %s", redisHint)
	}
	rdb, err := ratelimit.NewClient(url)
	if err != nil {
		t.Fatalf("%v (%s)", err, redisHint)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Fatalf("redis at %s is not reachable: %v (%s)", url, err, redisHint)
	}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	prefix := "test-" + hex.EncodeToString(b)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		iter := rdb.Scan(ctx, 0, prefix+":*", 100).Iterator()
		for iter.Next(ctx) {
			rdb.Del(ctx, iter.Val())
		}
		_ = rdb.Close()
	})
	return rdb, prefix
}
