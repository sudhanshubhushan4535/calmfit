// Command dlq-replay re-publishes workout-events-dlq records to workout-events, after the bug that
// sent them there is fixed. -dry-run lists them without changing anything.
//
//	go run ./cmd/dlq-replay -dry-run
//	go run ./cmd/dlq-replay [-max N]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/worker"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "list pending DLQ records; produce and commit nothing")
	max := flag.Int("max", 0, "replay at most this many records (0 = all pending)")
	flag.Parse()
	if err := run(*dryRun, *max); err != nil {
		log.Fatal(err)
	}
}

func run(dryRun bool, max int) error {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		return fmt.Errorf("KAFKA_BROKERS must be set (see .env.example)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := worker.NewConsumer(strings.Split(brokers, ","), worker.ReplayGroup, events.DLQTopic)
	if err != nil {
		return fmt.Errorf("kafka client: %w", err)
	}
	defer client.Close()

	rp := &worker.Replayer{Client: client, Admin: kadm.NewClient(client), DLQ: events.DLQTopic,
		Topic: events.Topic, DryRun: dryRun, Max: max, Out: os.Stdout}
	n, err := rp.Run(ctx)
	verb := "replayed"
	if dryRun {
		verb = "pending (dry run, nothing replayed)"
	}
	log.Printf("dlq-replay: %d record(s) %s", n, verb)
	return err
}
