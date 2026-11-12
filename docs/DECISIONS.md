# Decisions

Product questions the build spec doesn't answer. Each one takes the most conservative option;
revisit any of them by changing the code, tests and this file together.

## Day 3

### D1. Can "do later" move an exercise that already has logged sets?
- **Choice:** Yes. It only changes the order within today's workout.
- **Why:** Order has no effect on plans, sets or progression, so blocking it would protect nothing.

### D2. How is the new order stored?
- **Choice:** The moved exercise gets `max(position) + 1`, so positions can have gaps
  (the `0-based` comment in migration 00009 describes the initial order only).
- **Why:** Reads only `ORDER BY position`. Moving one row can't collide with the non-deferrable
  `UNIQUE (workout_id, position)`, and other rows are never rewritten.

### D3. Where do the plate-check weights and timezone live?
- **Choice:** `config/app.json` (path in `APP_CONFIG`, default `config/app.json`), validated at
  startup: unknown keys, an invalid timezone, non-positive points or a threshold outside 1–max all
  stop the server. Tests load the same file.
- **Why:** The spec says "config, not code". One reviewed file is the simplest config that can't
  silently drift from the tests.

### D4. Whose timezone is "the user's local date"?
- **Choice:** One app-wide zone from config (Asia/Kolkata). No per-user timezone column, and the
  client can't send its own offset.
- **Why:** The pilot is one gym in India. A per-user zone is a new feature, and trusting a client
  offset would let dates be gamed.

### D5. The spec's 5 example days (6/6/7/5/4 points)
- **Resolved:** `internal/plate/score_test.go` now uses the exact days from
  `docs/BUILD_SPEC.md`: protein ×3; both ×2 + nothing; protein ×3 + fiber ×1; fiber ×3 +
  protein ×1; protein ×2.

### D6. Which dates can be logged?
- **Choice:** Today and up to 7 days back (local). Future dates → 400. GET ranges are at most
  31 days and can't end in the future.
- **Why:** Lets people catch up on a missed day without allowing unlimited rewriting of history.

### D7. How do missing meals, percent and the streak work?
- **Choice:** A meal with no entry scores 0. Percent = points / max, rounded down (6/9 → 66%).
  The streak counts good days back from today. A day with **no plate entries at all is neutral**:
  it neither breaks nor extends the streak. Only a *logged* day under the good-day threshold
  breaks it, and today never breaks it while it's still in progress. Lookback is capped at 366 days.
  The weekly review's plate tip compares good days with *logged* days only, so a week with no
  entries gets no plate tip.
- **Why:** The spec says "Skipping a meal never resets the streak harshly; no guilt messaging" (user
  decision, Day 3). Rounding down never overstates.

### D8. What does "Protein ✓" mean on the plate check?
- **Choice:** The guide says to tick it for one protein-rich serving, or two moderate ones.
  Tier cut-offs (<5 g low, 5–10 g moderate, >10 g rich) are marked "verify before launch" like every other figure.
- **Why:** The spec names the tiers but not the rule. This is the simplest rule a gym member can apply at the table.

### D9. Is the protein guide public, and what else is on it?
- **Choice:** `GET /guide/protein` needs no sign-in. The wording follows the spec: isabgol is "a
  fiber supplement for days without fruit or vegetables, not a replacement for them", and "less
  gravy" gives the reason "most of the oil sits in the gravy". I added one line: "check with a
  doctor if you take regular medication".
- **Why:** It's static education with no user data. Psyllium can affect how medicines are absorbed,
  so the cautious wording is the safer default.

### D10. Where do exercise images and cues live, and who can see them?
- **Choice:** 2 JPEGs per exercise were downloaded once from free-exercise-db
  (github.com/yuhonas/free-exercise-db, Unlicense/public domain) into
  `internal/web/static/exercises/<id>/{0,1}.jpg`. They are embedded in the binary and served
  publicly at `/static/` with a 1-day cache. Seed validation rejects any other path, so nothing is
  hot-linked. `GET /exercises/{id}` needs sign-in.
- **Why:** Embedding means the server can't run with missing images. Images are public-domain demo
  pictures; the how-to endpoint sits behind auth like the rest of the API.

### D11. How are the cues marked as unreviewed?
- **Choice:** Exactly 2 cues per exercise (each at most 80 characters) in `seed/exercises.json`.
  The API returns `cues_status: "review"` for every exercise until a coach signs them off.
- **Why:** The spec says to mark cues "review". A per-exercise review flag would be a new
  workflow; one constant is the smallest honest marker.

### D12. What counts as "planned" for a past week?
- **Choice:** The user's *current* `days_per_week`, for every week shown.
- **Why:** v1 doesn't record when `days_per_week` changed. Storing that history would be a new feature.

### D13. How are workouts dated and bucketed in the progress charts?
- **Choice:** By `completed_at` in the config timezone; a week is Mon 00:00 to Sun 24:00 local.
  `GET /progress/{exerciseID}` returns set 1 of completed workouts only, the 50 most recent,
  oldest first. Weekly takes `weeks` 1–26 (default 8) ending with the current week. A user who
  isn't onboarded gets 409 `onboarding_required`.
- **Why:** In-progress sets aren't final. Bounded responses keep the test page and API cheap.

### D14. What counts as a lift that "went up"?
- **Choice:** The week's best set 1 (heaviest, then most reps) beats the last set 1 before the
  week: more weight, or the same weight for more reps. An exercise with no earlier set 1 isn't listed.
- **Why:** Set 1 is what the engine progresses on. A first-ever session isn't "up" from anything.

### D15. Where does "stalled" come from?
- **Choice:** For each exercise trained that week, the review runs `progression.Next` on history up
  to the end of Sunday and reports `Plan.Stalled`. The review never decides a stall on its own.
- **Why:** The spec says "flagged stalled by the engine". One source of truth, so the review can't
  disagree with the next workout.

### D16. Which tip is shown, and how is the plate scored for the week?
- **Choice:** One tip, the first rule that matches: no workouts → start with one; fewer than
  planned → consistency; any stall → "the plan has adjusted, focus on clean reps, sleep, protein";
  good plate days on fewer than half of the *logged* days → protein; otherwise "keep the same
  routine". Days with no plate entries are neutral (D7). The streak is as of
  Sunday for a past week, or today (unfinished) for the current one. Future dates → 400.
- **Why:** Showing up comes first. A stall tip never contradicts the engine's own fix. Counting
  days that haven't happened yet would punish a Tuesday review.

## Day 4

### D17. How do events leave the database?
- **Choice:** A transactional outbox. `outbox.Enqueue` takes a `pgx.Tx`, not a pool, so an event
  can only be written inside the transaction of the domain write it describes: `workout.logged`
  on the in_progress → completed transition, `plate.checked` on every plate upsert. A separate
  relay (`cmd/relay`) polls `SELECT … WHERE published_at IS NULL ORDER BY id LIMIT 100 FOR UPDATE
  SKIP LOCKED` every 500 ms, produces the batch synchronously (acks=all, idempotent producer),
  then sets `published_at` in the same transaction.
- **Why:** Writing to Postgres and Kafka separately (the dual-write problem) loses or invents
  events when one of the two fails. With the outbox, the event exists if and only if the write committed.
- **Trade-off:** At-least-once. If Kafka acks but the commit fails, the batch is published again.
  Consumers dedupe on `event_id`.

### D18. How many relays?
- **Choice:** Exactly one. Two are safe (`SKIP LOCKED` gives each row to one relay; tested), but
  they could publish one user's events out of order, because relay B can send row 150 before
  relay A sends row 50.
- **Why:** At pilot volume one relay has plenty of headroom, and per-user order comes for free.

### D19. Event shape
- **Choice:** The Kafka value is a JSON envelope `{event_id, type, version, user_id, occurred_at,
  data}` and the key is `user_id`. Types are `workout.logged` v1 (workout_id, day, completed_at,
  sets), `plate.checked` v1 (local_date, meal, protein, fiber) and `user.deleted` v1 (no data).
  The Day 3 hook comment said `workout.completed`; we use the spec's name.
- **Why:** The key keeps each user's events in one partition, in order. The version lets a payload
  change without breaking old consumers. Events carry facts only, never email or name.

### D20. Kafka setup
- **Choice:** One `apache/kafka:4.3.2` broker in KRaft mode, 512 MB heap, broker auto-create off.
  The host listener is **9094** (containers use `kafka:9092`). `make topics` creates
  `workout-events` (3 partitions) and `workout-events-dlq` (1 partition), both with
  `retention.ms=-1`. Client: franz-go (pure Go, explicit commits, admin API). Tests create and
  delete their own topics, and fail (never skip) when `TEST_KAFKA_BROKERS` is unset or unreachable.
- **Why:** 9094 avoids Postgres 5434, the API 8080, the old stack's 5433/6379, and the default
  9092. Infinite retention lets a full rebuild re-read from offset 0, and pilot volume is tiny.
  A DLQ record must not expire before someone looks at it.

### D21. Deleted users' events stay in Kafka (accepted for the pilot)
- **Choice:** With infinite retention, a deleted user's events (user_id plus training facts, no
  email or name) stay in `workout-events`. The worker skips events for deleted users, so their
  data is never rebuilt.
- **Why:** This is the simplest design that keeps replay working. It's pseudonymous data on a
  private broker for a 15–20 person pilot.
- **Future fix:** crypto-shredding (encrypt each payload with a per-user key and delete the key on
  account deletion), or a compacted design where a tombstone per key removes the data.

### D22. How long are published outbox rows kept?
- **Choice:** 7 days. The relay deletes `published_at < now() - 7 days` at most once a minute.
  Unpublished rows are never deleted. No index for this: at pilot volume the table holds about a
  week of rows.
- **Why:** Recent rows help debug "was it published?", and older ones are already in Kafka.

### D23. What does the worker build?
- **Choice:** One derived table, `weekly_stats` (user, Monday of the week in the config timezone,
  workouts, sets, volume_g = Σ weight_g × reps), built from `workout.logged`. `plate.checked` is
  validated and recorded as processed, with no projection (analytics come on Day 7).
  `user.deleted` deletes the user's `weekly_stats`. Nothing in the API reads `weekly_stats` yet.
- **Why:** This is the smallest projection that exercises the whole event path. Stall flags stay
  with the engine (D15), so the worker can't disagree with it.

### D24. How is each event applied exactly once?
- **Choice:** At-least-once delivery plus idempotent apply. In one DB transaction the worker
  (1) inserts `event_id` into `processed_events` with `ON CONFLICT DO NOTHING` (0 rows means a
  duplicate, so stop), (2) checks the user, (3) runs the handler. Kafka offsets are committed
  manually (`DisableAutoCommit`) only **after** that transaction commits. `BlockRebalanceOnPoll`
  keeps a polled batch's partitions until its offsets are committed.
- **Why:** Duplicates come from the relay (D17), a crash between the DB commit and the offset
  commit, and rebalances. `weekly_stats` increments on purpose, so a missing dedupe would show up
  as double counting. `TestDuplicateDeliveryIsAppliedOnce` covers a duplicate record and a
  simulated crash-then-redelivery.
- **On a transient error:** the worker commits the records before the failing one and stops, so
  the failing record is redelivered after a restart. (Commit 3 adds bounded retries and the DLQ.)
  Permanent errors (bad envelope, unknown type/version, invalid payload) are logged and skipped
  for now; commit 3 sends them to the DLQ.

### D25. Events for deleted or unknown users
- **Choice:** Before the handler runs, the worker reads the user with `FOR SHARE`. If the user is
  missing or soft-deleted, the event is recorded in `processed_events` and skipped, with no error.
  `user.deleted` always runs, because it only removes rows.
- **Why:** The worker must never crash or rebuild data for a deleted user, including on a full
  replay. The row lock serializes the event with account deletion (which locks the user `FOR
  UPDATE`): either the stats are written first and the deletion removes them, or the deletion
  wins and the event is skipped.
