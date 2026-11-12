package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

const kafkaHint = "run `make kafka-up`, set TEST_KAFKA_BROKERS (see .env.example), then `make test` (or `go test -short ./...` to skip)"

// Kafka is a broker plus a pair of topics that exist only for one test.
type Kafka struct {
	Brokers []string
	Topic   string // 3 partitions, like workout-events
	DLQ     string // 1 partition, like workout-events-dlq
}

// KafkaTopics connects to TEST_KAFKA_BROKERS and creates fresh per-test topics, deleted when the
// test ends, so tests never read or write the dev topics. Like DB, it fails, never skips, when
// Kafka isn't configured or reachable. Only `go test -short` skips.
func KafkaTopics(t *testing.T) Kafka {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Kafka-backed test in -short mode")
	}
	env := os.Getenv("TEST_KAFKA_BROKERS")
	if env == "" {
		t.Fatalf("TEST_KAFKA_BROKERS is not set: %s", kafkaHint)
	}
	k := Kafka{Brokers: strings.Split(env, ",")}

	cl, err := kgo.NewClient(kgo.SeedBrokers(k.Brokers...))
	if err != nil {
		t.Fatalf("kafka client: %v (%s)", err, kafkaHint)
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := adm.ListBrokers(ctx); err != nil {
		t.Fatalf("kafka at %s is not reachable: %v (%s)", env, err, kafkaHint)
	}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	base := "test-" + hex.EncodeToString(b)
	k.Topic, k.DLQ = base+"-events", base+"-dlq"
	for topic, parts := range map[string]int32{k.Topic: 3, k.DLQ: 1} {
		resp, err := adm.CreateTopic(ctx, parts, 1, nil, topic)
		if err == nil {
			err = resp.Err
		}
		if err != nil {
			t.Fatalf("create topic %s: %v", topic, err)
		}
	}
	t.Cleanup(func() {
		cl, err := kgo.NewClient(kgo.SeedBrokers(k.Brokers...))
		if err != nil {
			t.Errorf("kafka cleanup client: %v", err)
			return
		}
		defer cl.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := kadm.NewClient(cl).DeleteTopics(ctx, k.Topic, k.DLQ); err != nil {
			t.Errorf("delete topics %s, %s: %v", k.Topic, k.DLQ, err)
		}
	})
	return k
}

// ReadAll returns every record currently in topic: it reads from the start up to the end offsets
// taken at call time, so the count is exact (nothing still in flight is missed or invented).
func ReadAll(t *testing.T, brokers []string, topic string) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ends, err := kadm.NewClient(cl).ListEndOffsets(ctx, topic)
	if err != nil {
		t.Fatalf("end offsets %s: %v", topic, err)
	}
	var want int64
	ends.Each(func(o kadm.ListedOffset) { want += o.Offset })

	var out []*kgo.Record
	for int64(len(out)) < want {
		fs := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			t.Fatalf("read %s: got %d records, want %d", topic, len(out), want)
		}
		if err := fs.Err0(); err != nil {
			t.Fatalf("read %s: %v", topic, err)
		}
		out = append(out, fs.Records()...)
	}
	return out
}
