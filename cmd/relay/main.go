// Command relay publishes outbox rows to Kafka. Run exactly one instance (see internal/outbox).
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

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/outbox"
)

func main() {
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

	client, err := outbox.NewProducer(strings.Split(brokers, ","))
	if err != nil {
		return fmt.Errorf("kafka client: %w", err)
	}
	defer client.Close()
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("kafka ping: %w", err)
	}

	log.Printf("relay: publishing outbox to %s on %s", events.Topic, brokers)
	err = (&outbox.Relay{DB: pool, Client: client, Topic: events.Topic}).Run(ctx)
	log.Println("relay: stopped")
	return err
}
