#!/usr/bin/env bash
# EXPLAIN (ANALYZE, BUFFERS) of the benchmarked queries, with real parameters for one user
# (docs/BENCHMARKS.md). Usage: scripts/explain.sh [google_sub]  (default: the synthetic user with
# the most workouts). Needs psql and DATABASE_URL; works against any host.
set -euo pipefail
: "${DATABASE_URL:?DATABASE_URL is not set}"
PSQL=(psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1)

SUB="${1:-$("${PSQL[@]}" -tAc "SELECT u.google_sub FROM users u JOIN workouts w ON w.user_id = u.id
  WHERE u.google_sub ~ '^synthetic-[0-9]{5}\$' GROUP BY u.google_sub ORDER BY count(*) DESC, u.google_sub LIMIT 1")}"
UID_="$("${PSQL[@]}" -tAc "SELECT id FROM users WHERE google_sub = '$SUB'")"
EX="${EX:-Barbell_Squat}"
# Last full Mon–Sun week in IST, like the review's ?date=<last Sunday>
IFS='|' read -r FROM TO <<<"$("${PSQL[@]}" -tAc "SELECT
  (date_trunc('week', now() AT TIME ZONE 'Asia/Kolkata') - interval '7 days') AT TIME ZONE 'Asia/Kolkata',
  (date_trunc('week', now() AT TIME ZONE 'Asia/Kolkata')) AT TIME ZONE 'Asia/Kolkata'")"
echo "user $SUB ($UID_), exercise $EX, week [$FROM, $TO)"
echo "workouts: $("${PSQL[@]}" -tAc "SELECT count(*) FROM workouts WHERE user_id = '$UID_'")"

explain() {
  echo; echo "=== $1"; shift
  "${PSQL[@]}" -c "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) $*"
}

explain "history() — next workout and weekly review, once per exercise" "
SELECT w.completed_at, we.plan, array_agg(s.weight_g ORDER BY s.set_number), array_agg(s.reps ORDER BY s.set_number)
FROM (SELECT w.id, w.completed_at, w.started_at FROM workouts w
  WHERE w.user_id = '$UID_' AND w.status = 'completed' AND (NULL::timestamptz IS NULL OR w.completed_at <= NULL::timestamptz)
    AND EXISTS (SELECT 1 FROM workout_exercises we WHERE we.workout_id = w.id AND we.exercise_id = '$EX' AND we.plan IS NOT NULL)
    AND EXISTS (SELECT 1 FROM workout_sets s WHERE s.workout_id = w.id AND s.exercise_id = '$EX')
  ORDER BY w.completed_at DESC, w.started_at DESC LIMIT 2) w
JOIN workout_exercises we ON we.workout_id = w.id AND we.exercise_id = '$EX'
JOIN workout_sets s ON s.workout_id = w.id AND s.exercise_id = '$EX'
GROUP BY w.id, w.completed_at, w.started_at, we.plan
ORDER BY w.completed_at DESC, w.started_at DESC"

explain "history(upTo) — weekly review, as of the week's end" "
SELECT w.completed_at, we.plan, array_agg(s.weight_g ORDER BY s.set_number), array_agg(s.reps ORDER BY s.set_number)
FROM (SELECT w.id, w.completed_at, w.started_at FROM workouts w
  WHERE w.user_id = '$UID_' AND w.status = 'completed' AND ('$TO'::timestamptz IS NULL OR w.completed_at <= '$TO'::timestamptz)
    AND EXISTS (SELECT 1 FROM workout_exercises we WHERE we.workout_id = w.id AND we.exercise_id = '$EX' AND we.plan IS NOT NULL)
    AND EXISTS (SELECT 1 FROM workout_sets s WHERE s.workout_id = w.id AND s.exercise_id = '$EX')
  ORDER BY w.completed_at DESC, w.started_at DESC LIMIT 2) w
JOIN workout_exercises we ON we.workout_id = w.id AND we.exercise_id = '$EX'
JOIN workout_sets s ON s.workout_id = w.id AND s.exercise_id = '$EX'
GROUP BY w.id, w.completed_at, w.started_at, we.plan
ORDER BY w.completed_at DESC, w.started_at DESC"

explain "weekly review — last set 1 before the week, once per exercise" "
SELECT s.weight_g, s.reps FROM workouts w
JOIN workout_sets s ON s.workout_id = w.id AND s.set_number = 1
WHERE w.user_id = '$UID_' AND w.status = 'completed' AND w.completed_at < '$FROM' AND s.exercise_id = '$EX'
ORDER BY w.completed_at DESC, w.started_at DESC LIMIT 1"

explain "weekly review — the week's set 1 rows" "
SELECT e.id, s.weight_g, s.reps FROM workouts w
JOIN workout_sets s ON s.workout_id = w.id AND s.set_number = 1
JOIN exercises e ON e.id = s.exercise_id
WHERE w.user_id = '$UID_' AND w.status = 'completed' AND w.completed_at >= '$FROM' AND w.completed_at < '$TO'
ORDER BY w.completed_at, e.id"

explain "weekly review — plate days" "
SELECT local_date, meal, protein, fiber FROM plate_checks
WHERE user_id = '$UID_' AND local_date BETWEEN ('$FROM'::timestamptz AT TIME ZONE 'Asia/Kolkata')::date AND ('$TO'::timestamptz AT TIME ZONE 'Asia/Kolkata')::date"

explain "history endpoint — /progress/{exerciseID}" "
SELECT completed_at, weight_g, reps FROM (
  SELECT w.completed_at, w.started_at, s.weight_g, s.reps FROM workouts w
  JOIN workout_sets s ON s.workout_id = w.id
  WHERE w.user_id = '$UID_' AND w.status = 'completed' AND s.exercise_id = '$EX' AND s.set_number = 1
  ORDER BY w.completed_at DESC, w.started_at DESC LIMIT 50) recent ORDER BY completed_at, started_at"
