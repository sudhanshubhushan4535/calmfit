# Benchmarks

Every number here was measured; nothing is estimated. **All numbers so far are "local Mac".**
VM numbers (Oracle A1, 2 OCPU / 12 GB) come on Day 7 with the same scripts.

## Targets (fixed before the first run, 2026-11-12)

| Endpoint | Route | p95 | p99 | Errors |
| --- | --- | --- | --- | --- |
| Log a set | `POST /workouts/{id}/sets` | ≤ 50 ms | ≤ 100 ms | < 0.1% |
| Next workout | `GET /workouts/next` | ≤ 100 ms | ≤ 200 ms | < 0.1% |
| Weekly review | `GET /review/weekly` | ≤ 150 ms | ≤ 300 ms | < 0.1% |
| History | `GET /progress/{exerciseID}` | ≤ 50 ms | ≤ 100 ms | < 0.1% |

The targets are encoded as k6 thresholds in each script, so a run that misses one exits non-zero.

## Test shape

- **Load:** k6 constant-arrival-rate, **50 requests/s**. A 15 s warm-up is excluded from the
  thresholds, then 60 s are measured (`scenario:measure`). One endpoint per run.
- **Users:** 500 synthetic users (`USERS`), every 8th of the 4,000, so the history-length mix
  matches the data set. Each request picks a random user. `setup()` signs them in through test
  auth (D39), so the cookie lookup is part of every request, as in production.
- **Per endpoint:**
  - **Log a set:** setup starts (or resumes) a workout per user and logs sets 1–2 of its first
    exercise. Each request then upserts a random set 1–3 at the plan weight, which is the real
    write path (row lock, in-session check, upsert, response view).
  - **Weekly review:** half the requests are for the current week (Mon–Wed of real data plus today),
    half for last week (`?date=` last Sunday, a full week).
  - **History:** a random exercise of the 16.
- **Voice:** not benchmarked (the spec says load tests never call the real LLM).
- **Command:** run from the Mac: `make bench` (all four) or `make bench-log-set` etc.
  `BASE_URL` (default `http://localhost:8080`) points them anywhere. The API must run with
  `APP_ENV=dev TEST_AUTH=1`.

## Environment (local Mac)

- **Machine:** MacBook Air M2 (Mac14,2), 8 cores, 8 GB RAM, macOS 27.2.
- **Docker:** Docker Desktop 28.1.1 with 8 CPUs and 3.8 GB memory. Postgres 16 runs in Docker with
  default settings (`shared_buffers` 128 MB).
- **API:** a native binary (`go build`), not containerised. k6 v2.3.0 runs on the same machine, so
  the load generator competes with the API for CPU.
- **Data:** `make seed-synthetic` (D41): 4,000 users, 395k workouts, 2.1M exercise rows, 6.0M
  sets, 1.35M plate checks; the database is 2,803 MB.

## Results (local Mac, 2026-11-12)

All runs use 50 req/s and the 60 s measured window, with 0 errors in every run (0 of ~3,000
requests each). Latencies are k6 `http_req_duration` (client side, so they include HTTP and the
cookie/session lookup).

### Before

| Endpoint | p50 | p95 | p99 | max | vs target |
| --- | --- | --- | --- | --- | --- |
| Log a set, run 1 ¹ | 5.2 ms | 9.1 ms | 153.7 ms | 650 ms | ✗ p99 |
| Log a set, run 2 | 5.6 ms | 8.2 ms | 11.3 ms | 33 ms | ✓ |
| Next workout | 6.9 ms | 21.1 ms | 29.8 ms | 61 ms | ✓ |
| **Weekly review** | 21.1 ms | **1,480 ms** | **2,450 ms** | 3,180 ms | ✗ p95 and p99 |
| History | 4.3 ms | 10.8 ms | 16.1 ms | 61 ms | ✓ |

¹ Run 1 overlapped autovacuum of the freshly bulk-loaded tables (`pg_stat_user_tables`:
`workout_sets` vacuumed 20:53, during the run). Run 2 is the same code after it finished.

### What the plans showed

**`history()` (the engine's input) read every workout the user had ever done.** Next workout calls
it once per exercise of the day (5–6). The weekly review calls it once per exercise trained that
week (up to 16), next to a cheap "last set before the week" query. That N+1 was checked first, as
planned. The call count alone wasn't the problem: each call scanned the user's whole history.

The plan for the heaviest synthetic user (484 workouts), with Barbell_Squat:

<details><summary>Before: 2,447 buffers; 144.6 ms cold, 7.6–13.4 ms warm</summary>

```
 Limit (actual time=144.432..144.434 rows=2 loops=1)
   Buffers: shared hit=1576 read=871
   ->  Sort (actual time=144.431..144.433 rows=2 loops=1)
         Sort Key: w.completed_at DESC, w.started_at DESC
         Sort Method: top-N heapsort  Memory: 28kB
         ->  GroupAggregate (actual time=143.884..144.390 rows=156 loops=1)
               Group Key: w.id, we.plan
               ->  Sort (actual time=143.835..143.859 rows=461 loops=1)
                     Sort Key: w.id, we.plan, s.set_number
                     ->  Nested Loop (actual time=3.104..142.464 rows=461 loops=1)
                           ->  Nested Loop (actual time=2.933..126.265 rows=161 loops=1)
                                 ->  Bitmap Heap Scan on workouts w (actual time=0.078..0.370 rows=483 loops=1)
                                       Recheck Cond: (user_id = '…'::uuid)
                                       Filter: (status = 'completed'::text)
                                       ->  Bitmap Index Scan on workouts_user_completed_idx (rows=484 loops=1)
                                 ->  Index Scan using workout_exercises_pkey on workout_exercises we (actual time=0.260..0.260 rows=0 loops=483)
                                       Index Cond: ((workout_id = w.id) AND (exercise_id = 'Barbell_Squat'::text))
                                       Filter: (plan IS NOT NULL)
                                       Buffers: shared hit=880 read=730
                           ->  Index Scan using workout_sets_pkey on workout_sets s (actual time=0.098..0.099 rows=3 loops=161)
 Execution Time: 144.555 ms
```
</details>

The `GROUP BY` sat before the `LIMIT 2`. Postgres had to build all 156 sessions, probing
`workout_exercises` once for each of the 483 workouts, before it could keep 2. Cost grows with
the user's history, so the heaviest users made up the tail.

**Why there's no index:** the filter (`exercise_id`, on `workout_exercises`) and the sort
(`completed_at`, on `workouts`) are in different tables. No single index serves both, and adding
`user_id`/`completed_at` to `workout_exercises` would be denormalising for one query. Every other
benchmarked query already used an index and stopped early: the review's "before" query 0.3 ms,
its week rows 0.3 ms, plate days 0.06 ms, and the history endpoint 4 ms (it reads 154 workouts for
50 points, because squat is one day in three).

**Fix (query only, no schema change).** An inner query now picks the two sessions first. It walks
`workouts_user_completed_idx` backwards with `EXISTS` checks for the exercise's plan and sets, and
stops at two. Only those two are aggregated. The results are the same: the same sessions, the same
order, and the same plan/set filters. The existing integration tests for history, skipped
exercises and the review pass unchanged.

<details><summary>After: 78 buffers, 1.0–1.3 ms (7 workouts read instead of 483)</summary>

```
 GroupAggregate (actual time=0.793..0.802 rows=2 loops=1)
   Buffers: shared hit=77 read=1
   ->  Incremental Sort (actual time=0.741..0.744 rows=6 loops=1)
         ->  Nested Loop (actual time=0.565..0.578 rows=6 loops=1)
               ->  Nested Loop (actual time=0.528..0.534 rows=2 loops=1)
                     ->  Limit (actual time=0.507..0.509 rows=2 loops=1)
                           ->  Incremental Sort (actual time=0.506..0.507 rows=2 loops=1)
                                 Sort Key: w.completed_at DESC, w.started_at DESC
                                 Presorted Key: w.completed_at
                                 ->  Nested Loop (actual time=0.257..0.478 rows=3 loops=1)
                                       ->  Nested Loop Semi Join (actual time=0.159..0.347 rows=3 loops=1)
                                             ->  Index Scan Backward using workouts_user_completed_idx on workouts w (actual time=0.078..0.082 rows=7 loops=1)
                                                   Index Cond: (user_id = '…'::uuid)
                                                   Filter: (status = 'completed'::text)
                                             ->  Index Only Scan using workout_sets_pkey on workout_sets s_1 (rows=0 loops=7)
                                       ->  Index Scan using workout_exercises_pkey on workout_exercises we_1 (rows=1 loops=3)
                     ->  Index Scan using workout_exercises_pkey on workout_exercises we (rows=1 loops=2)
               ->  Index Scan using workout_sets_pkey on workout_sets s (rows=3 loops=2)
 Execution Time: 1.272 ms
```
</details>

**The N+1 is kept.** After the fix, each per-exercise call costs about 1 ms or less in the
database. A heavy user's review makes roughly two small queries per exercise trained, and its p99
is 26 ms against a 300 ms target. Batching them into one query would add a second, more complex
query shape for no measured need, so it wasn't done.

### After (the history() rewrite; no new index)

| Endpoint | Run | p50 | p95 | p99 | max | vs target |
| --- | --- | --- | --- | --- | --- | --- |
| Log a set | 1 ² | 5.4 ms | 11.2 ms | 105.2 ms | 1,260 ms | ✗ p99 |
| | 2 | 5.4 ms | 8.0 ms | 12.1 ms | 33 ms | ✓ |
| | 3 | 5.7 ms | 8.2 ms | 11.6 ms | 26 ms | ✓ |
| Next workout | 1 | 7.0 ms | 9.7 ms | 16.2 ms | 28 ms | ✓ |
| | 2 ² | 6.5 ms | 20.7 ms | 225.4 ms | 491 ms | ✗ p99 |
| | 3 ² | 7.0 ms | 10.6 ms | 36.8 ms | 450 ms | ✓ |
| | 4 | 7.0 ms | 9.4 ms | 13.6 ms | 41 ms | ✓ |
| **Weekly review** | 1 | 12.5 ms | **19.0 ms** | **25.5 ms** | 65 ms | ✓ |
| | 2 | 12.3 ms | 18.8 ms | 27.1 ms | 53 ms | ✓ |
| | 3 | 12.6 ms | 20.0 ms | 26.6 ms | 45 ms | ✓ |
| History (code unchanged) | 1 | 4.2 ms | 10.6 ms | 16.5 ms | 33 ms | ✓ |
| | 2 | 4.2 ms | 10.9 ms | 17.2 ms | 48 ms | ✓ |

² These runs had one short stall: 20–30 requests within about 1–2 s took 0.1–1.3 s, then latency
went back to normal. With per-request timestamps (`k6 --out csv`), the next-workout stall in run 3
was at 15:41:58–59 UTC. That didn't match a Postgres checkpoint (15:40:20–15:40:43, which a clean
log-set run overlapped) or any autovacuum/analyze (`pg_stat_user_tables`). The cause wasn't
identified. k6, the API, Docker Desktop's VM and Postgres all share one 8-core laptop, so these
p99 misses are reported as measured, not explained away. The Day 7 VM runs (k6 on the Mac, API on
the VM) will show whether they recur.

### Summary (local Mac)

| Endpoint | p95 before → after | p99 before → after |
| --- | --- | --- |
| Weekly review | 1,480 ms → 19.0–20.0 ms | 2,450 ms → 25.5–27.1 ms |
| Next workout | 21.1 ms → 9.4–20.7 ms | 29.8 ms → 13.6–225.4 ms (one stalled run) |
| Log a set | 8.2 ms (clean) → 8.0–11.2 ms | 11.3 ms → 11.6–105.2 ms (one stalled run) |
| History | 10.8 ms → 10.6–10.9 ms | 16.1 ms → 16.5–17.2 ms |

**Indexes added: none.** No plan showed a need. The fix was a query rewrite, measured above.

### Reproduce

```
make seed-synthetic                      # 4,000 users, seed 42 (D41)
APP_ENV=dev TEST_AUTH=1 make run         # in another terminal
make bench                               # or make bench-weekly-review, …
make explain                             # plans for the heaviest synthetic user (SUB=… for another)
```
