# CalmFit v1

Gym app backend: Google sign-in, a Push/Pull/Legs program, and a deterministic progression engine
that gives the weight and reps for every set, with a one-line reason. Go API + worker, Postgres,
outbox → Kafka, Redis (rate limiting only).

- Build spec: [docs/BUILD_SPEC.md](docs/BUILD_SPEC.md)
- Decisions: [docs/DECISIONS.md](docs/DECISIONS.md)
- Progression rules: [docs/progression.md](docs/progression.md)
- Benchmarks (k6, query plans): [docs/BENCHMARKS.md](docs/BENCHMARKS.md)
- Failure drills: [docs/DRILLS.md](docs/DRILLS.md)

The full README (architecture, schema, Kafka design, voice eval, deployment) is written with the
Day 7 release.

## Backups and restore

```
make backup         # pg_dump -Fc of DATABASE_URL → backups/calmfit-<UTC time>.dump + .counts.json
make restore-test   # restore the newest dump into a fresh DB, compare row counts per table, PASS/FAIL
make restore-test FILE=backups/calmfit-20261112T155906Z.dump   # a specific one
```

**How it works:**
- **Consistent counts:** `make backup` opens one REPEATABLE READ, read-only transaction, exports
  its snapshot, and runs `pg_dump --snapshot` on exactly that snapshot. It counts every table's
  rows inside the same transaction. The manifest (`*.counts.json`) therefore describes exactly
  what is in the dump, even while the app keeps writing.
- **Restore check:** `make restore-test` drops and recreates the scratch database
  `calmfit_restore_check` on the same server. It never touches the source database (it refuses
  that name). It then runs `pg_restore --no-owner --no-privileges --exit-on-error` and compares
  the table set and every table's row count with the manifest.
  - **PASS** drops the scratch database.
  - **FAIL** keeps it for inspection and exits non-zero.
- **Client tools:** these come from the `postgres:16` Docker image on the host network, so they
  match the server's major version and need no local install (D43). `PG_BIN=/path/to/bin` uses
  local binaries instead, and `PG_IMAGE` picks another image.
- **Host-agnostic:** both commands read only `DATABASE_URL`, so they run unchanged on the VM.
- **Storage:** `backups/` is gitignored. Nightly scheduling and off-VM copies are set up with the
  deployment (Day 7).

**Tested restore** (local Mac, 2026-11-12, dev DB with the synthetic data):

| Step | Result |
| --- | --- |
| Backup | 130.0 MB dump of a 2.8 GB database in 30.5 s; 14 tables, 9,933,365 rows |
| Restore into a fresh DB | 70.1 s |
| Row counts vs manifest | 14/14 tables match → **PASS** |

**To recover for real**, restore into a new database, check it, then point `DATABASE_URL` at it:

```
createdb calmfit_restored   # or: CREATE DATABASE calmfit_restored;
docker run --rm -i --network host postgres:16 pg_restore --no-owner --no-privileges --exit-on-error \
  --dbname=postgres://USER:PASSWORD@HOST:PORT/calmfit_restored < backups/calmfit-<time>.dump
```
