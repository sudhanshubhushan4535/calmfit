#!/usr/bin/env bash
# Drill: a malformed record and an unknown event version land in the DLQ; the valid event with
# the same key behind them is still applied.
NAME=malformed; source "$(dirname "$0")/common.sh"
build; guard
./bin/drill users -run "$RUN" -n 1
start_relay; start_worker 1 9102; start_worker 2 9103; wait_assigned
./bin/drill wait-idle
./bin/drill malformed -run "$RUN" | tee "$OUT/result.txt"
say "worker log lines for the DLQ:"
grep -h 'DLQ' "$OUT"/worker*.log | tail -2
