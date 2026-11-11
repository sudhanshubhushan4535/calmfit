# Load DATABASE_URL from .env if present (optional so CI can set env directly)
-include .env
export

.PHONY: db-up migrate-up migrate-down migrate-status seed

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
