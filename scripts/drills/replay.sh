#!/usr/bin/env bash
# Drill: full rebuild of weekly_stats by replaying workout-events (D28). TRUNCATEs weekly_stats
# and processed_events for ALL users, then rebuilds them; the before/after snapshots must match.
# A pg_dump of both tables is saved first, so a failed rebuild can be restored.
NAME=replay; source "$(dirname "$0")/common.sh"
PG_IMAGE="${PG_IMAGE:-postgres:16}"
build; guard
start_relay; start_worker 1 9102; start_worker 2 9103; wait_assigned
./bin/drill wait-idle
./bin/drill snapshot -out "$OUT/before.json"

say "safety copy of weekly_stats and processed_events"
docker run --rm --network host -v "$PWD/$OUT:/out" "$PG_IMAGE" \
  pg_dump -Fc -t weekly_stats -t processed_events -f /out/derived-before.dump "$DATABASE_URL" 2>/dev/null \
  || pg_dump -Fc -t weekly_stats -t processed_events -f "$OUT/derived-before.dump" "$DATABASE_URL"
ls -la "$OUT"/derived-before.dump

say "1. stop both workers"
for p in "${PIDS[@]:1}"; do kill -TERM "$p"; done
sleep 3
./bin/drill members
say "2. TRUNCATE weekly_stats, processed_events (one transaction)"
psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 -c 'BEGIN; TRUNCATE weekly_stats, processed_events; COMMIT;'
say "3. reset calmfit-stats offsets to earliest"
docker compose exec -T kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --group calmfit-stats --topic workout-events --reset-offsets --to-earliest --execute
say "4. start the workers"
START=$(date +%s)
start_worker 1 9102; start_worker 2 9103
./bin/drill wait-idle -timeout 10m
ELAPSED=$(( $(date +%s) - START ))
RECORDS=$(grep -hcE ' (applied|duplicate|skipped_user)$' "$OUT"/worker*.log | paste -sd+ - | bc)
DLQ=$(grep -hc '→ DLQ' "$OUT"/worker*.log | paste -sd+ - | bc)
say "rebuild: $RECORDS records applied/skipped + $DLQ to the DLQ in ~${ELAPSED}s"
./bin/drill snapshot -out "$OUT/after.json"
./bin/drill compare "$OUT/before.json" "$OUT/after.json" | tee "$OUT/result.txt"
