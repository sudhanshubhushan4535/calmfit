#!/usr/bin/env bash
# Validates docker-compose.prod.yml without deploying anything (D50):
#  1. `docker compose config` with dummy values for every required variable;
#  2. every service has a memory limit, and only nginx publishes a port beyond 127.0.0.1;
#  3. the long-running services (all profiles) fit the budget, leaving room for the OS page cache.
set -euo pipefail
cd "$(dirname "$0")/.."
BUDGET_MIB=${BUDGET_MIB:-6144} # of the VM's 12 GB; the rest is page cache, the OS and one-shot jobs

dummy=$(mktemp)
trap 'rm -f "$dummy"' EXIT
cat > "$dummy" <<'ENV'
POSTGRES_PASSWORD=dummy
GOOGLE_CLIENT_ID=dummy.apps.googleusercontent.com
DOMAIN=example.com
ENV

json=$(docker compose -f docker-compose.prod.yml --env-file "$dummy" --profile obs --profile tools config --format json)

missing=$(jq -r '.services | to_entries[] | select(.value.deploy.resources.limits.memory == null) | .key' <<<"$json")
if [ -n "$missing" ]; then
  echo "FAIL: no memory limit: $missing"; exit 1
fi

# Only nginx may listen beyond loopback; Postgres and Grafana bind to 127.0.0.1 (SSH tunnel).
exposed=$(jq -r '.services | to_entries[] | select(.key != "nginx") | .key as $s | (.value.ports // [])[] | select((.host_ip // "") != "127.0.0.1") | "\($s):\(.published)"' <<<"$json")
if [ -n "$exposed" ]; then
  echo "FAIL: published beyond loopback: $exposed"; exit 1
fi

# Long-running = restart policy other than "no" (one-shot jobs and cron tools run briefly).
jq -r '.services | to_entries[] | [.key, (.value.restart // "no"), (.value.deploy.resources.limits.memory|tonumber/1048576|floor), (.value.deploy.resources.limits.cpus // "-")] | @tsv' <<<"$json" \
  | sort | awk -v budget="$BUDGET_MIB" '
    BEGIN { printf "%-12s %-15s %8s %5s\n", "service", "restart", "MiB", "cpus" }
    { printf "%-12s %-15s %8d %5s\n", $1, $2, $3, $4; if ($2 != "no") steady += $3; else oneshot += $3 }
    END {
      printf "long-running total: %d MiB (budget %d MiB); one-shot/cron jobs: %d MiB, never all at once\n", steady, budget, oneshot
      if (steady > budget) { print "FAIL: over budget"; exit 1 }
      print "OK"
    }'
