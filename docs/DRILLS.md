# Failure drills

Every result below was measured on the **local Mac** (2026-11-12), using the same compose
services as on the VM (Postgres 16, Kafka 4.3.2 KRaft single broker, Redis 7.4).

**How the drills run:**
- Each drill is `make drill-*`. It builds the binaries, starts **its own relay and two workers**
  (logs in `drill-out/<run>/`, gitignored), and refuses to start while other workers or a relay
  are running.
- Drill events belong to dedicated users (`drill-<run>-NNN`). They're written with
  `outbox.Enqueue` in real transactions (the same path as completing a workout), so the expected
  `weekly_stats` totals are known exactly.
- `drill verify` compares those totals and the `processed_events` coverage with the database.

| Drill | Result | Key number |
| --- | --- | --- |
| Kill a worker mid-load | **PASS**: 12,000/12,000 events applied, totals exact, 0 duplicates | partition reassigned 46.1 s after `kill -9` |
| Stop Redis | **PASS**: voice 503 ×20; the other 4 endpoints 200 ×20 each | fail-closed voice answer takes ~0.5 s |
| Forced duplicate delivery | **PASS**: 103 duplicates skipped, totals unchanged | 103/103 counted as `duplicate` |
| Malformed event | **PASS**: 2 records → DLQ with headers; the next event on the same key applied | applied 660 ms later |
| Replay rebuild | **Not run yet:** needs approval (see below) | — |

## 1. Kill one worker mid-load: `make drill-worker-kill`

**Steps:**
1. Create 50 drill users. Start the relay and workers 1 and 2. Worker 1 got partition 0;
   worker 2 got partitions 1 and 2.
2. Enqueue 12,000 `workout.logged` events (15 sets each) at 200/s through the outbox.
3. After 20 s, `kill -9` worker 1. A crash: no graceful leave, no final commit.

| | Value |
| --- | --- |
| Events enqueued | 12,000 in 60.2 s (199/s) |
| Worker 1 killed | 21:24:20.994 (had applied 867 events) |
| Worker 2 revoked/assigned `[0]` | 21:25:07.098 / 21:25:07.119 → **46.1 s after the kill** |
| Drained after the last enqueue | 11.4 s (outbox empty, group lag 0) |
| `processed_events` | 12,000 / 12,000 |
| `weekly_stats` (drill users) | expected = observed: 12,000 workouts, 180,000 sets, 61,958,777,500 g |
| Duplicates redelivered to worker 2 | 0 |

**What it shows:**
- **Nothing is lost.** Partition 0's events waited in Kafka until worker 2 took the partition.
- **Nothing is counted twice.** Worker 1 commits a batch's offsets right after its DB
  transactions, so the uncommitted window at the kill held no applied records. If it had,
  `processed_events` would have turned them into duplicates.
- **The 46 s gap is Kafka's session timeout** (franz-go default 45 s). The broker waits that
  long before it declares a silent member dead. A graceful stop (SIGTERM, D24) leaves the group
  at once instead.
- The setting wasn't changed: lowering it trades faster failover for false evictions. For a
  stats projection, 46 s of extra lag on one partition is harmless.

## 2. Stop Redis: `make drill-redis`

**Harness (D42):** the real router, real Postgres and the real Redis limiter, with a **fake voice
parser** (never the LLM), in-process in `bin/drill`. It makes 20 requests per endpoint for a
synthetic user. The steps are Redis up → `docker compose stop redis` → `docker compose start redis`.

| Endpoint | Redis up | Redis down | Redis back |
| --- | --- | --- | --- |
| Voice parse | 200 ×20 (p50 1.7 ms) | **503 ×20 (p50 503.7 ms, max 508.4 ms)** | 200 ×20 (1.2 ms) |
| Log a set | 200 ×20 (1.7 ms) | 200 ×20 (3.8 ms) | 200 ×20 (1.5 ms) |
| Next workout | 200 ×20 (3.9 ms) | 200 ×20 (5.2 ms) | 200 ×20 (3.6 ms) |
| Weekly review | 200 ×20 (9.0 ms) | 200 ×20 (9.7 ms) | 200 ×20 (8.0 ms) |
| History | 200 ×20 (0.8 ms) | 200 ×20 (1.0 ms) | 200 ×20 (0.8 ms) |

**What it shows:**
- **Voice fails closed** (`voice_unavailable`), so no parse runs without the cost guard (D34).
- **Tap logging and every read are untouched.** They never call Redis.
- **Voice recovers by itself** when Redis returns, with no restart.

**Finding:** the fail-closed answer is not instant. With the port refusing connections, go-redis
still retries the dial inside its pool ("failed to dial after 5 attempts"), so each 503 used up
most of the limiter's 500 ms budget (0.42–0.51 s). The user sees a slow "voice unavailable" and
can tap instead. Nothing was changed on Day 6 (no new features). Capping the pool's dial retries
is a possible follow-up.

## 3. Forced duplicate delivery: `make drill-duplicate`

**Steps:**
1. Baseline: 500 events from 20 drill users, applied and verified.
2. Two kinds of duplicate:
   - **Relay re-publish:** `published_at` set back to NULL on 100 published rows, so the real
     relay publishes them a second time. This is what happens when Kafka acks but the outbox
     commit fails (D17).
   - **Broker-level:** one event produced 3 more times, byte for byte, straight to `workout-events`.

| | Value |
| --- | --- |
| Duplicates skipped (`calmfit_worker_events_total{outcome="duplicate"}`, both workers) | **103** (expected 103) |
| `weekly_stats` before → after | 500 workouts, 7,500 sets, 2,577,807,500 g → **unchanged** |
| Drained after the duplicates | 0.5 s |

**What it shows:** the `processed_events` insert (D24) turns every redelivery into a no-op, whether
it came from the relay or the broker.

## 4. Malformed event: `make drill-malformed`

**Steps:**
1. Two bad records, keyed with a real drill user's id (so they share that user's partition):
   - a truncated JSON value;
   - a valid envelope with `workout.logged` **v99** (no handler).
2. Then one valid event for the same user through the outbox.

| | Value |
| --- | --- |
| DLQ records | 2 → 4 (**+2**) |
| DLQ @2 | from `workout-events/0@2880`, attempts=1, `permanent: decode envelope: unexpected end of JSON input` |
| DLQ @3 | from `workout-events/0@2881`, attempts=1, `permanent: no handler for workout.logged v99` |
| Valid event behind them | applied **660 ms** after enqueue: +1 workout, +15 sets, +6,052,500 g |

**What it shows:** permanent errors skip the retries and go straight to the DLQ (D26), with the
source offset and the error, so a poison record never blocks its partition. `make dlq-replay`
(D27) can send them back after a fix.

## 5. Replay rebuild: `make drill-replay` (not run yet)

The script follows D28 exactly:
1. Start the relay and workers and wait until idle.
2. Snapshot `weekly_stats`: row count, sums, and the md5 of every row in key order.
3. Save a `pg_dump` of `weekly_stats` + `processed_events` into `drill-out/`.
4. Stop the workers.
5. `TRUNCATE weekly_stats, processed_events` in one transaction.
6. Reset `calmfit-stats` to the earliest offsets.
7. Start the workers, wait for lag 0, snapshot again, compare.

It hasn't been run on the dev database yet. Step 5 deletes the derived rows of **every** user, not
only synthetic or drill users, before rebuilding them, and that needs an explicit go-ahead
first. The result goes here once it has run.
