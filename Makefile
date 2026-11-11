# Load DATABASE_URL from .env if present (optional so CI can set env directly)
-include .env
export

.PHONY: db-up migrate-up migrate-down migrate-status seed run test-db test

db-up:
	docker compose up -d --wait postgres

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

# Create calmfit_test on the compose Postgres if missing, migrate it, and load the seed (idempotent)
test-db:
	docker compose exec -T postgres psql -U calmfit -d calmfit -tAc "SELECT 1 FROM pg_database WHERE datname = 'calmfit_test'" | grep -q 1 \
		|| docker compose exec -T postgres createdb -U calmfit calmfit_test
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/migrate up
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/seed

# .env is exported above, so DB-backed tests see TEST_DATABASE_URL
test:
	go test ./...
