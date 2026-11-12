#!/usr/bin/env bash
# Drill: stop Redis. Voice must fail closed (503); every other endpoint must be unaffected.
NAME=redis; source "$(dirname "$0")/common.sh"
: "${REDIS_URL:?REDIS_URL is not set}"
go build -o bin/drill ./cmd/drill

say "Redis up"
./bin/drill redis -phase up | tee "$OUT/up.txt"
say "docker compose stop redis"
docker compose stop redis
./bin/drill redis -phase down | tee "$OUT/down.txt"
say "docker compose start redis"
docker compose up -d --wait redis
./bin/drill redis -phase up | tee "$OUT/up-again.txt"
