# Load DATABASE_URL from .env if present (optional so CI can set env directly)
-include .env
export

.PHONY: db-up kafka-up topics migrate-up migrate-down migrate-status seed run relay worker dlq-replay test-db test

db-up:
	docker compose up -d --wait postgres

kafka-up:
	docker compose up -d --wait kafka

# Idempotent. Partitions and retention are explicit because broker auto-create is off.
# retention.ms=-1: a full rebuild re-reads workout-events from offset 0 (see docs/DECISIONS.md).
KAFKA_TOPICS = docker compose exec -T kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092
topics:
	$(KAFKA_TOPICS) --create --if-not-exists --topic workout-events --partitions 3 --config retention.ms=-1
	$(KAFKA_TOPICS) --create --if-not-exists --topic workout-events-dlq --partitions 1 --config retention.ms=-1

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

migrate-status:
	go run ./cmd/migrate status

# Validates seed/*.json, then upserts in one transaction; safe to re-run
seed:
	go run ./cmd/seed

run:
	go run ./cmd/api

# Publishes outbox rows to Kafka; run exactly one
relay:
	go run ./cmd/relay

# Builds weekly_stats from workout-events; run 2 (two terminals) to see the group share partitions
worker:
	go run ./cmd/worker

# Re-publish DLQ records to workout-events after a fix. List first: make dlq-replay ARGS=-dry-run
dlq-replay:
	go run ./cmd/dlq-replay $(ARGS)

# Create calmfit_test on the compose Postgres if missing, migrate it, and load the seed (idempotent)
test-db:
	docker compose exec -T postgres psql -U calmfit -d calmfit -tAc "SELECT 1 FROM pg_database WHERE datname = 'calmfit_test'" | grep -q 1 \
		|| docker compose exec -T postgres createdb -U calmfit calmfit_test
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/migrate up
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/seed

# .env is exported above, so DB-backed tests see TEST_DATABASE_URL
test:
	go test ./...
