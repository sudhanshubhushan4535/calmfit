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

## Make targets
- `db-up`: start Postgres and wait until healthy
- `migrate-up` / `migrate-down` / `migrate-status`: goose against `DATABASE_URL`
- `seed`: validate `seed/*.json`, upsert in one transaction (idempotent)
- `run`: start the API on :8080
- `test-db`: create, migrate and seed `calmfit_test`
- `test`: `go test ./...` with `.env` exported

## Tests
- DB tests use `calmfit_test` via `testutil.DB(t)`. If `TEST_DATABASE_URL` is unset they **fail,
  never skip** (hint: `make test-db`). Only `go test -short` skips them.
- Use `go vet` / `go test`, not `go build ./...` (it drops binaries in the repo root).

## Progression
`docs/progression.md` is the single source of truth (R1–R17). The engine in `internal/progression`
is deterministic: pure Go, no I/O, no clock, never an LLM. Change the doc, code and tests together.

## Working rules
- Commit directly to `main`; no branches unless asked.
- **Never commit or push without asking.**
- No new features beyond the build spec. Plan first; size indexes and features to v1 scope.
- Secrets live in `.env` (gitignored); keep `.env.example` in sync.
