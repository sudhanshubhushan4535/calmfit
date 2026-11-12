// Command worker consumes workout-events in group calmfit-stats and maintains weekly_stats.
// Run 2 instances; Kafka splits the 3 partitions between them and rebalances if one stops.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/metrics"
	"github.com/sudhanshubhushan4535/calmfit/internal/worker"
)

func main() {
	// Millisecond timestamps: the drills measure rebalance times from these logs.
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dsn := os.Getenv("DATABASE_URL")
	brokers := os.Getenv("KAFKA_BROKERS")
	if dsn == "" || brokers == "" {
		return fmt.Errorf("DATABASE_URL and KAFKA_BROKERS must be set (see .env.example)")
	}
	cfgPath := os.Getenv("APP_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/app.json"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("db pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("db ping: %w", err)
	}

	client, err := worker.NewConsumer(strings.Split(brokers, ","), worker.Group, events.Topic)
	if err != nil {
		return fmt.Errorf("kafka client: %w", err)
	}
	// Close leaves the group, so the other instance takes over these partitions at once.
	defer client.Close()

	// Default :9102; run the second instance with METRICS_ADDR=:9103 (D37).
	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":9102"
	}
	reg := metrics.NewRegistry()
	worker.RegisterMetrics(reg)
	reg.MustRegister(worker.NewKafkaCollector(kadm.NewClient(client), worker.Group, events.Topic, events.DLQTopic))
	if _, err := metrics.Start(ctx, metricsAddr, reg); err != nil {
		return err
	}

	log.Printf("worker: consuming %s in group %s (pid %d)", events.Topic, worker.Group, os.Getpid())
	err = (&worker.Worker{DB: pool, Client: client, Handlers: worker.DefaultHandlers(cfg),
		DLQTopic: events.DLQTopic}).Run(ctx)
	log.Println("worker: stopped")
	return err
}
