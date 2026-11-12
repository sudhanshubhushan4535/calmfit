#!/usr/bin/env bash
# Drill: forced duplicate delivery. The relay re-publishes 100 rows and one event is produced 3
# more times directly; weekly_stats must not change.
NAME=duplicate; source "$(dirname "$0")/common.sh"
build; guard
./bin/drill users -run "$RUN" -n 20
start_relay; start_worker 1 9102; start_worker 2 9103; wait_assigned
./bin/drill enqueue -run "$RUN" -events 500 -rate 250 -out "$OUT/expected.json"
./bin/drill wait-idle
say "baseline"
./bin/drill verify -run "$RUN" -expect "$OUT/expected.json"
D0=$(( $(metric 9102 duplicate) + $(metric 9103 duplicate) ))

say "force duplicates"
./bin/drill duplicate -run "$RUN" -republish 100 -times 3
./bin/drill wait-idle
sleep 1
D1=$(( $(metric 9102 duplicate) + $(metric 9103 duplicate) ))
say "duplicates skipped by the workers (calmfit_worker_events_total{outcome=\"duplicate\"}): $((D1 - D0)) (expected 103)"
./bin/drill verify -run "$RUN" -expect "$OUT/expected.json" | tee "$OUT/result.txt"
[ $((D1 - D0)) -eq 103 ] || { echo "RESULT: FAIL (duplicate count)"; exit 1; }
