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
- **Failures:** see D26 (bounded retries, then the DLQ).
- **Shutdown:** on SIGTERM the record in flight finishes and the batch so far is committed, then
  the worker leaves the group, so the other instance takes the partitions at once.

### D25. Events for deleted or unknown users
- **Choice:** Before the handler runs, the worker reads the user with `FOR SHARE`. If the user is
  missing or soft-deleted, the event is recorded in `processed_events` and skipped, with no error.
  `user.deleted` always runs, because it only removes rows.
- **Why:** The worker must never crash or rebuild data for a deleted user, including on a full
  replay. The row lock serializes the event with account deletion (which locks the user `FOR
  UPDATE`): either the stats are written first and the deletion removes them, or the deletion
  wins and the event is skipped.

### D26. What happens when an event keeps failing?
- **Choice:**
  - **Permanent errors** go to `workout-events-dlq` after 1 attempt. These are a bad envelope, an
    unknown type or version, or an invalid payload.
  - **Transient errors** (for example the DB) get **5 attempts** in place, with exponential backoff:
    waits of 200/400/800/1600 ms with jitter in [d/2, d), about 3 s at most. Then the record goes
    to the DLQ.
  - The DLQ record keeps the original key, value and headers, and adds `error`, `attempts`,
    `src-topic`, `src-partition`, `src-offset` and `failed-at`. The source offset is committed only
    after the DLQ produce is acknowledged.
  - **If the DLQ produce fails,** the worker commits nothing more and exits, so the record comes
    again after a restart.
  - A record that failed isn't written to `processed_events` (its transaction rolled back), so a
    replay can still apply it.
- **Why:** One poison message must not block its partition, and so every later event for that user.
  Bounded in-place retries ride out a DB blip without retry topics, and a ~3 s stall per bad record
  is fine at pilot volume. Retrying a permanent error is pointless.

### D27. How is the DLQ replayed?
- **Choice:**
  - `make dlq-replay` (`cmd/dlq-replay`) reads the DLQ in group `calmfit-dlq-replay`. It reads only
    up to the end offsets taken at start, so a record that fails again during the run isn't
    replayed in a loop.
  - Each record goes back to `workout-events` with its original key and value plus a
    `replayed-from-dlq` header (`<dlq>/<partition>@<offset>`). Its DLQ offset is committed only
    after the produce is acknowledged.
  - `ARGS=-dry-run` lists the records with their headers and changes nothing. `ARGS="-max N"`
    limits the run.
  - The command line was chosen over a UI, as the spec says.
- **Why:** Replay is a deliberate step after a fix, not automatic. At-least-once is safe because the
  worker dedupes on `event_id`, so replaying the same record twice applies it once (tested).
  A record that is still bad lands in the DLQ again, tagged as replayed.

### D28. Full rebuild of derived data (procedure, no tooling)
Use this after fixing a bug in derived logic, when the existing `weekly_stats` are wrong (rather
than just missing):
1. Stop both workers (Ctrl-C; they leave the group).
2. In one transaction: `TRUNCATE weekly_stats, processed_events;`
3. `docker compose exec kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --group calmfit-stats --topic workout-events --reset-offsets --to-earliest --execute`
4. Start the workers. They re-read the topic from offset 0. Infinite retention (D20) keeps the
   whole history there.

`processed_events` has to be cleared too, or every event would be skipped as a duplicate. Business
tables (workouts, sets, plate checks) are never touched. Deleted users' events are skipped again
(D25), so their data is never rebuilt. The rebuild drill ("stats match after rebuild") is part of
Day 6's failure drills.

### D29. What does account deletion do?
- **Choice:** `DELETE /me` (session user only) in **one transaction**:
  1. Lock the user row `FOR UPDATE`.
  2. Delete all sessions, which signs the user out on every device.
  3. Hard-delete `user_programs`, `workouts` (cascading to exercises and sets), `plate_checks`,
     `weekly_stats` and the user's outbox rows, published or not.
  4. Turn the users row into a tombstone: `deleted_at = now()`, `google_sub = NULL`, `email = ''`,
     `name = NULL`.
  5. Enqueue `user.deleted`, which is then the user's only outbox row.

  The response is 204 and clears the cookie. Migration 00014 adds
  `CHECK ((deleted_at IS NULL) = (google_sub IS NOT NULL))`, so a tombstone can never match a
  login. **The same Google account can sign up again as a brand-new user** with a new id and no
  old data. The Day 1 "account deleted" 403 branch and `ErrUserDeleted` are gone.
- **Why (user decision, Day 4):** The spec says "soft-delete + anonymise". We keep only a
  tombstone, which has no personal data. It means foreign keys never dangle, and the worker can
  tell "deleted" from "unknown" (D25). The training rows are deleted, not kept under a
  pseudonymous id, because an erasure request should erase. Taking the row lock serialises
  deletion with the worker, so a late event can't recreate stats.
- **Limits:** The deleted user's already-published events stay in Kafka (D21). The down migration
  for 00014 fails once any tombstone exists, because the original sub is gone by design.

### D30. What does the export contain?
- **Choice:** `GET /me/export` returns one JSON document as an attachment (`Cache-Control:
  no-store`), read in one REPEATABLE READ, read-only transaction so all sections are from the same
  moment. Sections:
  - `user` (id, email, name, created_at);
  - `program`;
  - `workouts`, each with its exercises, their plan snapshots and their sets;
  - `plate_checks`;
  - `weekly_stats`;
  - `sessions`: created and expiry times only.

  The session token hash is **never** exported. Every table is listed in exactly one of
  `account.ExportedTables`, `InternalTables` (outbox, processed_events, goose_db_version) or
  `ReferenceTables` (exercises, templates). A test fails if a new table is in none of them, or if
  an exported table isn't checked by the deletion test.
- **Why:** The outbox duplicates data that is already exported, and processed_events holds only
  event ids. Reference data isn't the user's. Building the JSON in SQL keeps the export to one
  query per section.

## Day 5

### D31. How does a voice parse become sets?
- **Choice:** The parser returns per-set `{weight | null, reps}`, an optional exercise and a unit.
  `voice.Resolve` (pure Go) then:
  1. **Exercise.** Uses the exercise that was said. If none was said, it uses the one open on screen
     (with a warning). It must be in today's open workout, and its usual-weight question must be answered.
  2. **Set numbers.** Sets are numbered from the first unlogged set, so "9 10" after set 1 is
     logged becomes sets 2 and 3. More sets than are left → `too_many_sets`.
  3. **Weights.** A set with no weight said takes the last weight said before it in the same phrase,
     so "bench 60, 8 8 7" logs all three at 60. A set before any weight was said takes the plan's
     target for that set, after in-session adjustment (R10). Each set says where its weight came
     from (`said` / `plan`).
  4. **Units.** No unit means kg. kg is kept to the gram. lb is converted and rounded to 0.1 kg,
     with a warning.
  5. **Ranges.** The same as tap logging: reps 1–50, weight > 0 and at most 1000 kg
     (`progression.MaxWeightG`, which now holds the cap `workout` used before).
- **Why (user decision, Day 5):** The program uses ascending weights per set, so the spec's single
  `weight` can't describe most sessions. Carrying a spoken weight forward matches how people talk.
  Falling back to the plan means "12 10 8" alone logs the prescribed session. The confirm screen
  shows every set's weight, and the user can edit any of them before saving, so a wrong guess is
  never saved silently.

### D32. What voice data is kept or sent out?
- **Choice:** Nothing new is stored: no transcript, parse or audio table. Audio never leaves the
  browser (Web Speech API). The LLM receives only the transcript and today's exercises (id, name,
  aliases), never a user id, email or name.
- **Why:** With nothing stored, account deletion and export (D29, D30) need no changes. The eval set
  (Day 5, C5) is a reviewed file in the repo, not harvested user speech.

### D33. Which LLM parses voice, and how is it called?
- **Choice:**
  - **Model:** Claude Haiku 5.5 (`claude-haiku-5-5`, set in `config/app.json` → `voice.model`), called
    with the official Go SDK (`anthropic-sdk-go/v2`).
  - **Request settings:** thinking disabled, effort `low`, `max_tokens` 512. JSON-schema structured
    output (`output_config.format`): `exercise_id` is an enum of today's exercise ids plus `""`, and
    each set's `weight` is a number or null. `understood: false` marks text that isn't a set log.
  - **Request contents:** a fixed system prompt, today's exercises (id, name, aliases) and the
    transcript, which is wrapped in tags and treated as data.
  - **Timeouts:** one **6 s deadline** covers the whole parse. Each attempt gets the smaller of
    5 s and the time left. There is at most one retry, and only for a retryable failure (the attempt
    timed out, a network error, 408/409/429/5xx). It happens only if at least 2 s remain after a
    200 ms backoff. The SDK's own retries are off, because it can't see the deadline. In practice a
    fast 5xx gets a second try, and a first attempt that times out at 5 s doesn't. A caller that
    disconnects is never retried.
  - **Isolation:** the client ignores environment defaults (`WithoutEnvironmentDefaults`), so
    `ANTHROPIC_BASE_URL` or a local profile can't redirect it.
  - **Outcomes:** a refusal, a `max_tokens` cut-off or `understood: false` is `could_not_parse`
    (the user says it again or taps). A failed call, or a reply that breaks the schema (re-checked
    in Go), is a provider error (502).
  - **Key:** `ANTHROPIC_API_KEY` lives only in `.env`. The API refuses to start with
    `voice.enabled: true` and no key. Tests use a fake server and never call the real API.
- **Why (user decision, Day 5):** The spec asks for a small, cheap model. Extraction needs no
  reasoning, so thinking off cuts latency and cost. The enum makes an exercise outside today's
  workout impossible. There are three different enums, one per PPL day; each schema is compiled
  once and then cached for 24 h.
- **Cost estimate (to be replaced by measured numbers):** about 1.5k input and 150 output
  tokens a call at $0.10 / $0.50 per MTok is about $0.0002 a call. 20 users × 10 calls × 30 days
  is about $1–2 a month.
- **Spending cap (manual step):** set a monthly limit for the key's workspace in the Anthropic
  Console before turning the flag on. The per-user daily limit (D34) bounds normal use, and the
  cap bounds everything else.

### D34. How is voice rate-limited?
- **Choice:**
  - **Limit:** `ratelimit.Daily` allows `voice.daily_limit` (30) parses per user per local
    calendar day (the config timezone, D4).
  - **Mechanism:** one `MULTI` runs `INCR <prefix>:<user>:<local date>` plus a `PEXPIRE` until the
    next local midnight. Because the expiry is relative, it isn't affected by clock skew between
    the API and Redis. Every attempt counts, including refused ones. INCR is atomic, so concurrent
    calls can't get past the limit (tested with 50 at once).
  - **Fails closed:** Redis is reached with 500 ms timeouts and no client retries. Any Redis error
    is `ErrUnavailable`, and the caller must refuse (503). Tap logging never touches Redis.
  - **Redis setup:** `redis:7.4` on host port 6380 with `maxmemory 200mb` and `noeviction`, so a
    full Redis refuses writes instead of silently dropping counters. There's no persistence.
- **Why (user decision, Day 5):** The spec says Redis is for rate limiting only. Failing closed
  means an outage can't remove the cost guard. Voice is optional, because every set can still be
  tapped.
- **Accepted:** restarting Redis resets that day's counters. That gives each user at most one more
  day's allowance (about $0.006). The provider spending cap (D33) bounds the rest.
- **Account deletion and export:** the key holds only a count and a pseudonymous user id, and
  expires by local midnight. `DELETE /me` doesn't touch Redis, and the export doesn't include it.

### D35. The voice endpoint
- **Choice:** `POST /workouts/{id}/voice/parse` takes `{transcript, exercise_id?}` and returns
  `{exercise_id, exercise_name, sets[{set_number, weight_g, reps, weight_source}], warnings}`.
  `exercise_id` is the exercise open on screen. It is used only when no exercise was said.
  - **Request checks, in order:**
    1. The route exists only when `voice.enabled` is true and the API was given a parser and a
       limiter (`server.WithVoice`). Otherwise it's a 404.
    2. Sign-in (401).
    3. Body: the transcript must be 1–200 characters after trimming, and unknown fields are rejected (400).
    4. The workout must belong to the user (another user's workout is the same 404 as a missing one).
    5. The workout must be open (409 `workout_completed`).
    6. Rate limit: 429 `voice_daily_limit`, or 503 `voice_unavailable` while Redis is down.
    7. Then the parser and `Resolve`.

    A request that fails one of steps 1–5 never reaches the limiter, so it doesn't use up the allowance.
  - **Errors after the checks:** 422 `{error, message}` for parses the user can fix (codes in
    `internal/voice`), 502 `voice_parser_unavailable` for a provider failure, and 500 for anything else.
  - **Read-only:** the handler reads the workout with `loadWorkout` and never opens a write
    transaction. The test page's Confirm sends each set to the existing `POST /workouts/{id}/sets`
    (an idempotent upsert). If a request fails, a retry continues from the first set that wasn't saved.
- **Why (user decision, Day 5):** A single write path means voice can't break set validation, the
  "set n−1 first" rule, in-session adjustment or the `workout.logged` outbox event. Tests check that
  a voice-confirmed workout emits the same event sets as a tapped one, and that parsing leaves every
  user row (workouts, exercises, sets, outbox, program) byte-for-byte unchanged. The trade-off is
  that a three-set confirm is three requests and not atomic. A partial save is visible and can be
  retried, and nothing is lost.
- **API startup:** with the flag on, `ANTHROPIC_API_KEY` and `REDIS_URL` are required. Redis isn't
  pinged at startup, because the API runs without it and only voice fails closed.
