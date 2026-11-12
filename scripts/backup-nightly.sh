#!/usr/bin/env bash
# Nightly Postgres backup on the VM (D50). It runs the tested `backup` command (D43) in the backup
# image: pg_dump -Fc from the same snapshot as an exact per-table row-count manifest, written to
# $BACKUP_DIR. Then it deletes dumps older than $BACKUP_KEEP_DAYS days.
#
# Install (VM clock is UTC; 20:45 UTC = 02:15 Asia/Kolkata, after the last workouts of the day):
#   crontab -e
#   45 20 * * * /opt/calmfit/scripts/backup-nightly.sh >> /var/log/calmfit/backup.log 2>&1
#
# Restore check (run it after the first nightly backup and after any change to the schema):
#   docker compose -f docker-compose.prod.yml --env-file .env.prod -p calmfit --profile tools \
#     run --rm --entrypoint restore-test backup -file /backups/<name>.dump
# These dumps stay on the VM's disk; copying them off the VM is an open deploy decision.
set -euo pipefail
cd "$(dirname "$0")/.."

ENV_FILE=${ENV_FILE:-.env.prod}          # overridable for a local smoke test
COMPOSE_PROJECT=${COMPOSE_PROJECT:-calmfit}

# Read only the two settings this script needs; compose reads the rest of the env file itself.
env_value() { grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true; }
BACKUP_DIR=$(env_value BACKUP_DIR); BACKUP_DIR=${BACKUP_DIR:-/var/backups/calmfit}
KEEP_DAYS=$(env_value BACKUP_KEEP_DAYS); KEEP_DAYS=${KEEP_DAYS:-7}
case $KEEP_DAYS in ''|*[!0-9]*) echo "BACKUP_KEEP_DAYS must be a number" >&2; exit 1;; esac

umask 077
mkdir -p "$BACKUP_DIR"
echo "== $(date -u +%FT%TZ) backup to $BACKUP_DIR"
BACKUP_DIR=$BACKUP_DIR docker compose -f docker-compose.prod.yml --env-file "$ENV_FILE" -p "$COMPOSE_PROJECT" \
  --profile tools run --rm -T backup

# Rotation only after a successful dump (set -e), and never the newest file.
newest=$(ls -1t "$BACKUP_DIR"/calmfit-*.dump | head -1)
find "$BACKUP_DIR" -maxdepth 1 -type f \( -name 'calmfit-*.dump' -o -name 'calmfit-*.counts.json' \) \
  -mtime +"$KEEP_DAYS" ! -name "$(basename "$newest")" -print -delete
echo "== done: $(basename "$newest")"
