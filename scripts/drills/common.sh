# Shared helpers for the failure drills (docs/DRILLS.md). Sourced, not run.
# Each drill builds the binaries, starts its own relay and two workers (logs in drill-out/), and
# stops them on exit. Host-agnostic: DATABASE_URL, KAFKA_BROKERS, REDIS_URL from the environment.
set -euo pipefail
cd "$(dirname "$0")/../.."
: "${DATABASE_URL:?DATABASE_URL is not set}" "${KAFKA_BROKERS:?KAFKA_BROKERS is not set}"

NAME="${NAME:?}"
RUN="$(date +%m%d%H%M%S)"
OUT="drill-out/${RUN}-${NAME}"
mkdir -p "$OUT"
PIDS=()

say() { printf '\n[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }

build() {
  go build -o bin/drill ./cmd/drill
  go build -o bin/worker ./cmd/worker
  go build -o bin/relay ./cmd/relay
}

# guard refuses to run next to other consumers in the group or another relay.
guard() {
  ./bin/drill members
  if pgrep -f 'cmd/relay|bin/relay' >/dev/null; then
    echo "a relay is already running; stop it first (the drill starts exactly one)"; exit 1
  fi
}

start_relay() {
  ./bin/relay >"$OUT/relay.log" 2>&1 &
  PIDS+=($!)
}

# start_worker N PORT; sets WORKER_PID
start_worker() {
  METRICS_ADDR=":$2" ./bin/worker >"$OUT/worker$1.log" 2>&1 &
  WORKER_PID=$!
  PIDS+=("$WORKER_PID")
}

# wait_assigned: until every partition is assigned across the two workers' logs
wait_assigned() {
  for _ in $(seq 1 60); do
    if [ "$(grep -h 'worker: assigned' "$OUT"/worker*.log 2>/dev/null | wc -l)" -ge 2 ]; then return 0; fi
    sleep 0.5
  done
  echo "workers did not get partitions"; exit 1
}

stop_all() {
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill -TERM "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap stop_all EXIT

# metric PORT OUTCOME: this worker's calmfit_worker_events_total for the outcome (0 if absent)
metric() {
  curl -s "localhost:$1/metrics" | awk -v o="outcome=\"$2\"" '$0 ~ "^calmfit_worker_events_total" && index($0, o) {print $2; f=1} END {if (!f) print 0}'
}
