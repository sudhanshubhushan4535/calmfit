#!/usr/bin/env bash
# Drill: kill -9 one of two workers mid-load. Expect a rebalance and nothing lost or double counted.
NAME=worker-kill; source "$(dirname "$0")/common.sh"
EVENTS="${EVENTS:-12000}"; RATE="${RATE:-200}"; KILL_AFTER="${KILL_AFTER:-20}"

build; guard
./bin/drill users -run "$RUN" -n 50
start_relay
start_worker 1 9102; W1=$WORKER_PID
start_worker 2 9103
wait_assigned
grep -h 'worker: assigned' "$OUT"/worker*.log

say "enqueue $EVENTS events at $RATE/s through the outbox; kill -9 worker 1 after ${KILL_AFTER}s"
./bin/drill enqueue -run "$RUN" -events "$EVENTS" -rate "$RATE" -out "$OUT/expected.json" &
ENQ=$!
sleep "$KILL_AFTER"
KILLED_AT="$(date +'%H:%M:%S').$(perl -MTime::HiRes=time -e 'printf "%03d", (time*1000)%1000')"
kill -9 "$W1"
say "killed worker 1 (pid $W1) at $KILLED_AT; partitions it held: $(grep 'worker: assigned' "$OUT/worker1.log" | tail -1 | sed 's/.*assigned //')"
wait "$ENQ"
say "enqueue finished; waiting for the outbox to drain and lag to reach 0"
./bin/drill wait-idle -timeout 5m

say "worker 2 group events after the kill:"
grep -E 'worker: (assigned|revoked)' "$OUT/worker2.log" | tail -4
say "duplicates seen by worker 2 (redelivered after the kill): $(grep -c ' duplicate$' "$OUT/worker2.log" || true)"
say "records applied: worker 1 $(grep -c ' applied$' "$OUT/worker1.log" || true), worker 2 $(grep -c ' applied$' "$OUT/worker2.log" || true)"
./bin/drill verify -run "$RUN" -expect "$OUT/expected.json" | tee "$OUT/result.txt"
