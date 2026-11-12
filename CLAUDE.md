# CalmFit v1

Gym app backend: Google sign-in, a Push/Pull/Legs program, and a progression engine that gives
the weight and reps for every set, with a one-line reason. Built as an interview portfolio piece
and for a small gym pilot.

## Stack
- Go (net/http 1.22+ route patterns), Postgres 16 via docker-compose, pgx/v5 + pgxpool
- Migrations: pressly/goose, plain SQL in `migrations/`
- Auth: Google sign-in (ID tokens) + server-side cookie sessions (only the token's SHA-256 is stored)

## Ports
- Postgres: **5434** (dev DB `calmfit`, test DB `calmfit_test`)
- API: **8080**. The old `calmfit-backend` container also uses 8080 and must stay stopped.
- Kafka: **9094** from the host (`kafka:9092` inside compose).
- Redis: **6380** from the host (rate limiting only). 6379 belongs to the old stack and a local redis-server.
- Metrics (D37): API **9101**, workers **9102** / **9103** (`METRICS_ADDR`). Prometheus **9095**, Grafana **3001** (loopback only).

## Make targets
- `db-up`: start Postgres and wait until healthy
- `redis-up`: start Redis (rate limiting only) and wait until healthy
- `kafka-up` / `topics`: start the broker; create `workout-events` and `workout-events-dlq` (idempotent)
- `obs-up` / `obs-down`: Prometheus + Grafana (compose profile `obs`), one provisioned dashboard + uptime alert
- `relay`: publish outbox rows to Kafka (run exactly one)
- `worker`: consume `workout-events` in group `calmfit-stats` into `weekly_stats` (run 2; the second with `METRICS_ADDR=:9103`)
- `dlq-replay`: re-publish DLQ records after a fix; `ARGS=-dry-run` lists them, `ARGS="-max N"` limits
- `migrate-up` / `migrate-down` / `migrate-status`: goose against `DATABASE_URL`
- `seed`: validate `seed/*.json`, upsert in one transaction (idempotent)
- `seed-synthetic`: 4,000 deterministic synthetic users via COPY, no outbox (D40/D41); `ARGS=-reset` to replace
- `run`: start the API on :8080 (`APP_ENV=dev TEST_AUTH=1 make run` for k6; D39)
- `bench` / `bench-<name>`: k6 benchmarks (docs/BENCHMARKS.md); `explain`: query plans for the heaviest synthetic user
- `drill-worker-kill` / `drill-redis` / `drill-duplicate` / `drill-malformed` / `drill-replay`: docs/DRILLS.md
  (each starts its own relay + 2 workers; `drill-replay` truncates derived tables for all users)
- `backup` / `restore-test`: pg_dump into ./backups with a row-count manifest; restore into a fresh DB and compare (D43, README)
- `test-db`: create, migrate and seed `calmfit_test`
- `test`: `go test ./...` with `.env` exported, then `node --test` for the share-card JS

## Tests
- DB tests use `calmfit_test` via `testutil.DB(t)`. If `TEST_DATABASE_URL` is unset they **fail,
  never skip** (hint: `make test-db`). Only `go test -short` skips them.
- Kafka tests use `testutil.KafkaTopics(t)` (per-test topics) and likewise fail without `TEST_KAFKA_BROKERS`.
- Redis tests use `testutil.Redis(t)` (per-test key prefix) and likewise fail without `TEST_REDIS_URL`.
- Use `go vet` / `go test`, not `go build ./...` (it drops binaries in the repo root).

## Progression
`docs/progression.md` is the single source of truth (R1–R17). The engine in `internal/progression`
is deterministic: pure Go, no I/O, no clock, never an LLM. Change the doc, code and tests together.

## Working rules
- Commit directly to `main`; no branches unless asked.
- **Never commit or push without asking.**
- No new features beyond the build spec. Plan first; size indexes and features to v1 scope.
- Secrets live in `.env` (gitignored); keep `.env.example` in sync.
