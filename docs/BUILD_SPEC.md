# CalmFit v1 — Build Spec

Nov 11, 2026 · @sudhanshu

## Goals and principles

CalmFit v1 is built in about one week, mainly as an interview portfolio project, with one honest final try at real users. Tagline: **Stop counting grams. Start getting stronger.**

1. **Interview first.** Every engineering piece must be explainable with a reason and a measured number.
2. **One final user try.** 15–20 people from the gym and reels, 4 weeks, success decided by pre-set metrics.
3. **Freeze the scope, not the quality.** No new features after v1 until real users have used it for 2+ weeks.
4. **Add a component only when a measurement or a real use case justifies it.**
5. **Honest numbers only.** Say "load-tested to X", never "X users". Only metrics that were actually measured go on the resume.
6. **Target user:** Indian vegetarian gym beginners and intermediates. Simple for the user, sophisticated underneath.

## v1 scope

Everything below is in v1 unless listed under Out. Next-lift targets stay free; there is no paywall in v1.

| Area | In v1 |
| --- | --- |
| Training | Onboarding, rotating PPL routine, one-tap set logging, deterministic progression engine, "why this weight" line, "next time" screen after every workout, "do later" for busy equipment, exercise instructions + 2 cues + 2 static images |
| Diet | Daily plate check (weighted score), streak, static veg protein guide |
| Progress | Weekly review (with stall detection), 1–2 strength charts, weekly Instagram share card |
| Account | One-tap Google sign-in, reminders (asked after first workout), account deletion + JSON export |
| AI | Voice logging via LLM, behind a flag until eval accuracy is acceptable |
| Web | Landing page, installable PWA, analytics events |
| Backend | Go API + worker, Postgres, Redis (rate limiting only), outbox + Kafka, k6 benchmarks, failure drills, Prometheus + basic Grafana, backups + restore test, LLM eval set, auth and progression-safety tests, input validation |

**Out of v1:** referral program, challenges/leaderboard, Razorpay/payments, founder-filmed videos, offline sync, image animation, exercise alternatives/swaps, Redis caching, local LLM, rule-based voice parser.

## Training: routine and exercises

One rotating Push → Pull → Legs cycle. The app shows **"your next workout"** = the one after the last completed workout, never tied to a weekday. 3 gym days a week = P, P, L; 4 days = P, P, L, P and next week starts at Pull. Missed days break nothing.

- Next workout index = `(last_completed_index + 1) % 3`
- One fixed exercise per spot. **No alternatives or swaps.** "Do later" only moves an exercise to the end of today's list.
- Validation rule: no two exercises in one day share both a `target_muscle` and a `movement_type`. `target_muscle` is our own fine-grained tag (e.g. front\_delt / side\_delt / rear\_delt), because the dataset's `primaryMuscles` is too coarse ("shoulders"). Compound + isolation on the same muscle is allowed (e.g. squat + leg extension). The seed also prints a coverage report of muscles no exercise targets (warning only).
- Images and instructions come from [free-exercise-db](https://github.com/yuhonas/free-exercise-db) (Unlicense, public domain, commercial use allowed). Download the images to our own storage; never hot-link.
- Store the standard name as the display name and the user's own names as aliases (used by search and voice logging).

| Day | Display name | Dataset ID | User's alias |
| --- | --- | --- | --- |
| Push | Incline Dumbbell Press | `Incline_Dumbbell_Press` | incline dumbell |
| Push | Machine Chest Fly (Pec Deck) | `Butterfly` | machine chest fly |
| Push | Dumbbell Shoulder Press | `Dumbbell_Shoulder_Press` | shoulder dumbell press |
| Push | Dumbbell Lateral Raise | `Side_Lateral_Raise` | dumbbell lateral raise |
| Push | Triceps Pushdown | `Triceps_Pushdown` | triceps pushdown |
| Pull | Lat Pulldown (shoulder-width grip) | `Close-Grip_Front_Lat_Pulldown` | lat pulldown |
| Pull | Seated Cable Row | `Seated_Cable_Rows` | seated cable row |
| Pull | Barbell Upright Row | `Upright_Barbell_Row` | barbell upright row |
| Pull | Rear Delt Machine Fly | `Reverse_Machine_Flyes` | rear delt machine flyes |
| Pull | Hammer Curl | `Hammer_Curls` | hammer curl |
| Pull | Dumbbell Preacher Curl | `Two-Arm_Dumbbell_Preacher_Curl` | dumbell preacher curl |
| Legs | Barbell Squat | `Barbell_Squat` | squat |
| Legs | Hip Thrust | `Barbell_Hip_Thrust` (verify at seed) | hip thrust |
| Legs | Seated Hamstring Curl | `Seated_Leg_Curl` | hamstring curls |
| Legs | Leg Extension | `Leg_Extensions` | leg extension |
| Legs | Seated Calf Raise | `Seated_Calf_Raise` | seated calve raise |

Templates are seed data (JSON/YAML loaded into Postgres), not code. Schema sketch: `exercises` (id, name, aliases, equipment, primary\_muscles, muscle\_size (big/small), target\_muscle, movement\_type (compound/isolation), image paths, instructions), `program_templates` (id, days ordered), `template_exercises` (template, day, order, exercise), `user_programs` (user, template, last\_completed\_index).

## Progression engine

The engine is **deterministic Go code** (never an LLM) and the core of the app. Every rule below needs unit tests.

| Rule | Logic |
| --- | --- |
| Working sets | 3 sets, rep targets **12 → 10 → 8**, ascending weight (e.g. 10 → 12.5 → 15 kg) |
| Step size | 2.5 kg (barbell), next dumbbell pair, one machine pin |
| Warm-up | App suggests \~50% and \~75% of set 1's weight, 2 reps each, rounded to the step. Not logged |
| Increase | Set 1 beats 12 reps (13+) → next session **all 3 sets move up one step together** |
| Building zone | After an increase, 10–12 reps on the 12-rep set = progress; repeat the weight |
| Failure | Below target − 2 reps (e.g. under 10 on the 12-rep set) |
| Missed target | Same weight next session |
| 2 failures in a row | Drop weight one step |
| In-session adjustment | Depends on muscle size; see "Big vs small muscles" below. Small muscles: if a set misses its target, the next set **does not go up in weight**: it stays at that weight and aims for the missed set's target reps (e.g. set 2 got 8 of 10 → set 3 same weight, target 10) |
| Progress | More reps or more weight than last session on that exercise |
| Stall | 3 sessions in a row with no progress on that exercise → flag it |
| Stall fix | Short by 1–2 reps → lower rep targets by 2 (10/8/6) at the same weight; short by more → drop one step; then build back up |
| First session (cold start) | Each exercise asks "What weight do you usually lift?"; "I don't know" → lightest option (empty bar / lightest dumbbell / lowest pin). Next time starts from the heaviest set actually completed |

### Big vs small muscles after a weight increase

Each exercise carries a `muscle_size` flag (big / small). The rule after a weight increase differs:

- **Big muscles (chest, back, legs):** if set 1 at the new weight reaches only 10 instead of 12, **shift all rep targets down by 2 → 10 / 8 / 6**, weights still ascending. Build back up to 12 / 10 / 8 at those weights before the next increase.
- **Small muscles (shoulders, traps, biceps, triceps):** set 1 at the new weight gets, say, 10. Set 2 still goes up one step and gets, say, 7–8. **Set 3 does not go up in weight**: it stays at set 2's weight and aims for 10 reps. Keep building until set 1 reaches 12 again; **only then does set 3 use its proper heavier weight** (one step above set 2).

| Exercise | Size |
| --- | --- |
| Incline Dumbbell Press | Big |
| Machine Chest Fly | Big |
| Lat Pulldown | Big |
| Seated Cable Row | Big |
| Barbell Squat | Big |
| Hip Thrust | Big |
| Seated Hamstring Curl | Big |
| Leg Extension | Big |
| Seated Calf Raise | Big (legs group), high-rep scheme 18 / 15 / 12 |
| Dumbbell Shoulder Press | Small |
| Dumbbell Lateral Raise | Small |
| Barbell Upright Row | Small |
| Rear Delt Machine Fly | Small |
| Triceps Pushdown | Small |
| Hammer Curl | Small |
| Dumbbell Preacher Curl | Small |

### Rep schemes per exercise

Each exercise carries a `rep_scheme` and a `rep_step` (in config/seed data, not code). Every rule above uses the scheme's numbers instead of the fixed 12 / 10 / 8.

| Scheme | Rep targets (sets 1 / 2 / 3) | rep\_step | Used by |
| --- | --- | --- | --- |
| Standard | 12 / 10 / 8 | 2 | All exercises except calves |
| High-rep | 18 / 15 / 12 | 3 | Seated Calf Raise |

- **Increase:** set 1 beats its top target (13+ standard, 19+ high-rep) → all sets up one step.
- **Building zone:** top target − rep\_step up to the top target (10–12 standard, 15–18 high-rep).
- **Failure:** below top target − rep\_step (under 10 standard, under 15 high-rep).
- **Big-muscle shift after an increase:** all targets drop by rep\_step (standard → 10 / 8 / 6, high-rep → 15 / 12 / 9), then build back up.
- **Stall fix (short by a little):** lower the targets by rep\_step, same rule.

**Extra test case:** calf raise follows the high-rep scheme end to end (increase at 19, building zone 15–18, shift to 15 / 12 / 9).

**Extra test cases:** big muscle shifts to 10/8/6 and builds back · small muscle keeps set 3 at set 2's weight until set 1 hits 12 · the switch back to the normal set-3 weight happens exactly when set 1 reaches 12.

Every target shows a one-line **"why this weight"**, e.g. "+2.5 kg: you hit 13 reps last time" or "Hold: set 3 missed its reps".

**Required test cases:** first workout with no history · all targets hit · set 1 beats 12 (all sets step up) · building zone (10–11 reps) · one failure · two failures in a row (drop) · in-session adjustment · skipped exercise · "do later" reorder · 3-session stall (rep-target fix and weight-drop fix) · no weight jump larger than one step · invalid input rejected (negative weight, zero or huge reps).

## Plate check and protein guide

No calorie or gram counting. After each of 3 meals the user taps Protein ✓ and Fiber ✓. Protein carries about two-thirds of the weight.

- **Points per meal:** Protein ✓ = 2, Fiber ✓ = 1. Max per day = 9.
- **Good plate day** = 6+ points (about 67%). Streak counts good plate days. Shown to the user as a daily score % (points ÷ 9).
- Weights (2, 1) and threshold (6) live in **config**, not code.
- Skipping a meal never resets the streak harshly; no guilt messaging.

| Day | Points | Good day? |
| --- | --- | --- |
| Protein in all 3 meals, no fiber | 6 | Yes |
| 2 meals with both, 1 meal nothing | 6 | Yes |
| Protein in all 3, fiber in 1 | 7 | Yes |
| Fiber in all 3, protein in 1 | 5 | No |
| Protein in 2 meals only | 4 | No |

**Static veg protein guide page** (linked from the plate check): practical portions of paneer, soya chunks (dry vs cooked), dal, curd and whey, labelled low / moderate / protein-rich meal. Also: less gravy (most oil sits there); fat is not the enemy; isabgol is a fiber supplement for days without fruit or vegetables, not a replacement for them. Approximate portion figures to be filled in before launch.

## Onboarding, first-session aha and retention

The aha moment: **"I don't have to decide what to lift next time. CalmFit already knows."** It must happen at the end of the very first workout.

1. **Sign-in:** one-tap Google sign-in. No email/password forms.
2. **Onboarding:** pick gym days per week → routine assigned. Show the one-line rotation explainer: "Your workouts follow Push → Pull → Legs. Your next workout is whatever comes after your last one."
3. **First workout:** each exercise asks the usual weight inline (optional; beginner default = lightest option). Logging is one tap ✓ per set, +/− steppers when different, no keyboard.
4. **"Next time" screen** after every workout: previous vs next target per exercise + the "why this weight" line.
5. **Reminders:** ask for push permission only *after* the first completed workout. Workout-day reminder, e.g. "Pull day today. Beat 45 kg on the lat pulldown." (Web push works on Android; iPhone only after Add to Home Screen.)
6. **Busy equipment:** "Do later" moves the exercise to the end of today's list.
7. **Weekly review:** workouts done vs planned, lifts that went up, lifts that stalled, plate streak, one tip for next week. Feeds the share card.
8. **Share card:** auto-generated Instagram-story image each week (workouts, plate score, strength gained), with Share / Download.
9. **Never lose a workout:** every set saved immediately; refresh or a failed request never loses logged data.
10. **PWA install:** small "Add to home screen" guide; tested on a real iPhone and Android phone.

**In-person setup at the gym (about 3 minutes):** ask "Abhi weight aur reps kaise track karte ho?", listen, show the "next time" screen, install it together, set the routine, log their current workout together. Later ask only: "Did you open it for your next workout without me reminding you?"

## Voice logging

The user says "bench 60, 8 8 7" and confirms the parsed sets with one tap. It's LLM-only for v1 and ships behind a flag; it is turned on only if eval accuracy is acceptable.

1. **Speech-to-text** in the browser (Web Speech API, free).
2. **LLM structured extraction** via a hosted API (small, cheap model; plan API credits or a few rupees a month) into a JSON schema: exercise, weight, unit, reps\[\].
3. **JSON Schema validation**, then **business validation** in Go: the exercise exists in today's plan (aliases help), weight and reps are in a sane range, units are right.
4. **One-tap confirm**, then save. Never save silently; on doubt, show the parsed sets for editing.

- Go `Parser` interface from day one (`APIParser` now; a `RuleParser` can be added later only if cost or latency data says so).
- Per-user daily limit on the endpoint (Redis rate limiter) + a spending cap in the LLM provider dashboard.
- Load tests use a **mock parser**, never the real API.
- **Eval set:** about 100 real gym phrases with expected output. Measure speech-to-text accuracy and LLM extraction accuracy **separately**, plus latency and cost per call. Only measured numbers go on the resume.

## Architecture and hosting

Go API + Go worker, Postgres as the source of truth, Redis for rate limiting only, an outbox relay into Kafka, and a hosted LLM API for voice parsing, all in Docker Compose on an Oracle Cloud Always Free VM (Ampere A1, 2 OCPU / 12 GB, Ubuntu 24.04, home region India South, Hyderabad).

&#91;embedded content: CalmFit v1 architecture · request path and event path\]

A user's request goes phone → nginx → Go API. Every write lands in Postgres together with an outbox row; the relay publishes it to Kafka, and two workers update derived stats or send failures to the DLQ.

- **Frontend:** mobile-first installable PWA; landing page as designed on the CalmFit canvas.
- **Build for Arm:** `GOARCH=arm64`, multi-arch Docker images.
- **Rough memory budget:** Kafka \~1 GB, Postgres 1–2 GB, Redis \~200 MB, two Go services \~100 MB.
- **Open ports 80/443** in both the Oracle security list and the VM firewall; HTTPS via Let's Encrypt.
- **Cost:** ₹0 hosting within Always Free limits; the only running cost is the LLM API (a few rupees a month or plan credits).

## Events: outbox, Kafka, replay

The Postgres outbox makes publishing reliable; Kafka provides fan-out and replay. Interview line: "At Spydra I built Kafka consumers and a Kafka integration path for banks; in CalmFit I went deeper into offsets, idempotency, replay and DLQs."

- **Outbox:** the workout write and an `outbox` row commit in **one DB transaction**. A relay publishes outbox rows to Kafka (can start as a polling loop). The relay may publish twice, so consumers must be idempotent.
- **Topics:** `workout-events` (3 partitions, key = `user_id` so each user's events stay ordered) and `workout-events-dlq`. Single broker, KRaft mode, heap capped at about 512 MB–1 GB.
- **Events:** `workout.logged`, `plate.checked`, `user.deleted`. Each carries an `event_id`.
- **Consumers:** 2 worker instances in one consumer group (watch rebalancing). **Manual offset commit only after the DB write succeeds** (at-least-once).
- **Idempotency:** a processed-events table / unique constraint on `event_id`; duplicates are skipped.
- **DLQ:** an event that keeps failing goes to `workout-events-dlq` instead of blocking its partition.
- **Replay (documented procedure, no UI):** fix a bug in derived logic → wipe derived tables (weekly stats, PRs, stall flags) → reset the consumer group's offsets to the start → rebuild. Business records are never duplicated.
- **Account deletion:** soft-delete + anonymise the user's rows; the worker skips events from deleted users (and on replay), so deleted data is never rebuilt.

## Testing, load, failure drills, observability

Every claim in an interview must trace back to a measurement recorded here.

- **Unit tests:** progression engine (all cases above), plate scoring, input validation.
- **Auth and authorization tests:** no user can ever read or change another user's workouts, stats or plate checks.
- **Synthetic data:** a Go generator producing realistic workout history for many fake users. Size it to the queries being tested, not a headline number.
- **k6 benchmarks:** pick 3–4 real queries (log a set, get next workout, weekly review, history). Set p95/p99 targets first, then measure, then record query plans and indexes before and after. Run k6 from the Mac, not the VM.
- **Failure drills (record results):** kill a worker mid-load (rebalance, nothing lost) · stop Redis · force duplicate delivery (no double counting) · send a malformed event (lands in DLQ) · replay rebuild (stats match).
- **Observability:** Prometheus metrics (API latency, error rate, Kafka consumer lag) + one basic Grafana dashboard + an uptime alert. No dashboard polishing.
- **Backups:** nightly `pg_dump` to storage + **one real restore test** into a fresh database.
- **Release checks:** deployment smoke tests; PWA install on a real iPhone and Android phone.

## Analytics and launch pilot

The final user try is a 4-week pilot with 15–20 people from the gym and reels; success is decided before it starts.

**Events to track:** `signup_completed`, `first_workout_completed`, `second_workout_completed`, `workout_completed`, `next_target_viewed`, `plate_checked`. Opening the app is not retention; only workout activity counts.

| Metric | Definition | Target |
| --- | --- | --- |
| First-workout activation | Signups completing a workout within 24 hours | 70%+ |
| Second workout | Activated users with another workout within 7 days | 60%+ |
| Week-2 retention | Activated users logging a workout on days 8–14 | 30%+ |
| Pilot success | Users still logging workouts in week 4 | At least 5 people |

- Onboard every pilot user in person; then don't chase them.
- **If the pilot hits the goal:** keep improving using the analytics. **If not:** leave it running free, stop investing time, and move on; the interview goal is already met.
- After v1 ships: no new features until real users have used it for at least 2 weeks.
- Week-2 candidates (only after the pilot data): referral, plateau tips beyond v1, founder exercise videos, exercise swaps (1–2 curated alternatives per slot; only if 3+ pilot users ask to change an exercise), pricing test (₹99–149/month, ₹799–999/year as hypotheses).

## 7-day plan and cut order

The target is 1 week; slipping into a second week is acceptable, adding new features is not. Claude Code writes the code; you review and own every module.

1. **Day 0 (today):** Oracle account, VM (Ampere A1, 2 OCPU / 12 GB, Ubuntu 24.04), Docker + Compose, ports 80/443 open. Progression rules written.
2. **Day 1 — Core backend:** schema, Google sign-in, onboarding, templates seed data, auth tests.
3. **Day 2 — Workout engine:** set logging, progression engine + all its tests, "next time" screen, "why this weight".
4. **Day 3 — User experience:** plate check + scoring, protein guide, exercise instructions + images, charts, weekly review.
5. **Day 4 — Distributed systems:** outbox, Kafka, 2 workers, idempotency, manual commits, DLQ, replay procedure, account deletion.
6. **Day 5 — Applied AI:** voice logging pipeline, validation, eval set measured.
7. **Day 6 — Reliability:** synthetic data, k6 with p95/p99 targets, failure drills, Prometheus + Grafana, backups + restore test.
8. **Day 7 — Release:** PWA, landing page, share card, reminders, analytics, deploy, smoke tests, real-phone install tests.

**If behind, cut in this order:** share card → voice logging → Grafana polish → failure-drill extras. **Never cut:** logging, progression engine and its tests, outbox + idempotency, k6 benchmarks with measured numbers, backups + restore test.

**Before the pilot (by trial day 30):** upgrade Oracle to Pay As You Go with a ₹1 budget alert, or keep a background job running so the VM never looks idle.

## README and interview checklist

The GitHub README is the interview script: every section answers a question an interviewer will ask.

- [ ] Architecture diagram and event flow
- [ ] Database schema
- [ ] Why the transactional outbox (the dual-write problem)
- [ ] Kafka: idempotency strategy, manual commits, DLQ, replay procedure and a real replay story
- [ ] Kafka vs a simpler outbox-only design: when each fits, and why Kafka here
- [ ] k6 results: queries, p95/p99 before and after, query plans, indexes added
- [ ] Failure drill results
- [ ] Voice logging: pipeline, eval method, measured accuracy, latency, cost
- [ ] Account deletion with event replay
- [ ] Deployment and recovery instructions (incl. tested restore)
- [ ] Pilot results (activation, week-2 and week-4 retention), whatever they turn out to be

**Resume bullet template (fill only with measured numbers):** "Built voice workout logging: LLM structured extraction with JSON schema and business validation, one-tap confirm, per-user rate limiting; \[X\]% accuracy on a \[100\]-phrase eval set at \~₹\[Z\]/month."
