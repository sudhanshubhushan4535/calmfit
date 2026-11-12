# Load DATABASE_URL from .env if present (optional so CI can set env directly)
-include .env
export

.PHONY: backup restore-test drill-worker-kill drill-redis drill-duplicate drill-malformed drill-replay bench bench-log-set bench-next-workout bench-weekly-review bench-history explain db-up kafka-up redis-up obs-up obs-down topics migrate-up migrate-down migrate-status seed seed-synthetic run relay worker dlq-replay voice-eval test-db test

db-up:
	docker compose up -d --wait postgres

kafka-up:
	docker compose up -d --wait kafka

redis-up:
	docker compose up -d --wait redis

# Prometheus (http://localhost:9095) + Grafana (http://localhost:3001, admin / GRAFANA_ADMIN_PASSWORD).
# Profile "obs": they start only here, never with the other services.
obs-up:
	@test -n "$(GRAFANA_ADMIN_PASSWORD)" || { echo "set GRAFANA_ADMIN_PASSWORD in .env"; exit 1; }
	docker compose --profile obs up -d --wait prometheus grafana

obs-down:
	docker compose --profile obs stop prometheus grafana

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

# Deterministic synthetic history for load tests (D40, D41): COPY, no outbox, refuses over ~3 GB.
# ARGS=-reset replaces existing synthetic users; also -users N, -seed S, -anchor YYYY-MM-DD.
seed-synthetic:
	go run ./cmd/seed-synthetic $(ARGS)

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

# Voice eval (D36). Dry run by default; ARGS=-yes calls the real API (costs money);
# ARGS="-stt eval/voice/stt.jsonl" scores captured speech-to-text without calling anything.
voice-eval:
	go run ./cmd/voice-eval $(ARGS)

# Create calmfit_test on the compose Postgres if missing, migrate it, and load the seed (idempotent)
test-db:
	docker compose exec -T postgres psql -U calmfit -d calmfit -tAc "SELECT 1 FROM pg_database WHERE datname = 'calmfit_test'" | grep -q 1 \
		|| docker compose exec -T postgres createdb -U calmfit calmfit_test
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/migrate up
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/seed

# .env is exported above, so DB-backed tests see TEST_DATABASE_URL
# Go tests, then the browser-side JS tests (share card) with Node's built-in runner
test:
	go test ./...
	node --test internal/web/*.test.cjs

# k6 benchmarks (docs/BENCHMARKS.md). The API must run with APP_ENV=dev TEST_AUTH=1 on BASE_URL.
# Summaries go to bench-results/ (gitignored). A missed target makes k6 exit non-zero.
K6 = mkdir -p bench-results && k6 run --quiet
bench-log-set:
	$(K6) --summary-export bench-results/log_set.json k6/log_set.js
bench-next-workout:
	$(K6) --summary-export bench-results/next_workout.json k6/next_workout.js
bench-weekly-review:
	$(K6) --summary-export bench-results/weekly_review.json k6/weekly_review.js
bench-history:
	$(K6) --summary-export bench-results/history.json k6/history.js
# Runs all four even if one misses its target
bench:
	-$(MAKE) bench-log-set
	-$(MAKE) bench-next-workout
	-$(MAKE) bench-weekly-review
	-$(MAKE) bench-history

# EXPLAIN (ANALYZE, BUFFERS) of the benchmarked queries for one user (default: the synthetic user
# with the most workouts). Uses psql against DATABASE_URL.
explain:
	scripts/explain.sh $(SUB)

# Failure drills (docs/DRILLS.md). Each starts its own relay + 2 workers and refuses to run while
# other workers or a relay are running. Logs in drill-out/ (gitignored).
drill-worker-kill:
	scripts/drills/worker-kill.sh
drill-redis:
	scripts/drills/redis.sh
drill-duplicate:
	scripts/drills/duplicate.sh
drill-malformed:
	scripts/drills/malformed.sh
# TRUNCATEs weekly_stats + processed_events for every user and rebuilds them from Kafka (D28)
drill-replay:
	scripts/drills/replay.sh

# Backups (README "Backups and restore", D43): pg_dump -Fc into ./backups (gitignored) with a
# row-count manifest from the same snapshot. Client tools come from the postgres:16 image unless
# PG_BIN points at local ones. Nightly scheduling is set up on the VM.
backup:
	go run ./cmd/backup

# Restores the newest backup (or FILE=…) into a fresh calmfit_restore_check database and compares
# every table's row count with the manifest; PASS drops it, FAIL keeps it.
restore-test:
	go run ./cmd/restore-test $(if $(FILE),-file $(FILE))
