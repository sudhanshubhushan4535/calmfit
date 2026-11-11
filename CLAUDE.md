# CalmFit v1

Gym app backend: Google sign-in, a Push/Pull/Legs program, and a deterministic progression
engine that tells you the weight and reps for every set, with a one-line reason.

## Stack
- Go (net/http with 1.22+ route patterns), Postgres 16 via pgx/v5 + pgxpool
- Migrations: pressly/goose, plain SQL in `migrations/`
- Auth: Google ID tokens (`google.golang.org/api/idtoken`) + server-side cookie sessions (SHA-256 of token stored)
- No Redis/Kafka/LLM. Add infra only when asked.

## Ports
- Postgres: **5434** (docker-compose; dev DB `calmfit`, test DB `calmfit_test`)
- API: **8080**. Another project's `calmfit-backend` container also uses 8080, so stop it first if it's running.

## Make targets
- `db-up`: start Postgres and wait for healthy
- `migrate-up` / `migrate-down` / `migrate-status`: goose against `DATABASE_URL`
- `seed`: validate `seed/*.json`, upsert in one transaction (idempotent)
- `run`: start the API on :8080
- `test-db`: create, migrate and seed `calmfit_test`
- `test`: `go test ./...` with `.env` exported

## Rules
- **DB tests fail, never skip**, when `TEST_DATABASE_URL` is unset (hint: run `make test-db`). Only `go test -short` skips them. Use `testutil.DB(t)`.
- **Never commit or push without asking.**
- Plan first for each task; size indexes and features to v1 scope.
- Use `go vet` / `go test`, not `go build ./...` (it drops binaries in the repo root).
- Secrets live in `.env` (gitignored); keep `.env.example` in sync.

## Progression
`docs/progression.md` is the single source of truth for the progression rules (R1–R17).
Change the doc, `internal/progression` and its tests together.
