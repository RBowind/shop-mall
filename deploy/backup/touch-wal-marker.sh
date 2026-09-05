#!/usr/bin/env bash
# touch-wal-marker.sh — the at-least-hourly WAL-archive freshness job.
#
# Runs pg_switch_wal(), waits for the archiver to copy the rotated segment, then
# refreshes <BACKUP_ROOT>/state/last-wal.marker with the ACTUAL archiver time.
# preflight.sh reads that marker for its "wal archive age <= 1h" gate.
#
# backup-db.sh only runs daily, so WITHOUT this hourly job the marker goes stale
# roughly 1h after the daily backup and preflight fails all day on a healthy
# host. Install it as the hourly cron (backup-db.sh prints the line; the Runbook
# documents it). The marker reflects pg_stat_archiver.last_archived_time, not a
# heartbeat: if archiving is actually broken, the marker ages and preflight
# fails, which is the correct alert.
#
# Usage:
#   touch-wal-marker.sh [--backup-root DIR] [--compose-file FILE]
#                       [--project NAME] [--container NAME] [--app-image TAG]
set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

require_cmd docker jq

BACKUP_ROOT="${BACKUP_ROOT:-/var/backups/shop-mall}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose/docker-compose.yml}"
PROJECT="${PROJECT:-}"
APP_IMAGE="${APP_IMAGE:-unknown}"
CONTAINER=""
WAIT_ARCHIVE_SECONDS="${WAIT_ARCHIVE_SECONDS:-20}"

usage() {
  cat <<'EOF' >&2
usage: touch-wal-marker.sh [options]

options:
  --backup-root DIR     backup store containing state/last-wal.marker
  --compose-file FILE   compose file defining the postgres service
  --project NAME        compose project name
  --container NAME      use a named postgres container instead of compose
  --app-image TAG       recorded into the marker as app_version
  --help                show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --backup-root) BACKUP_ROOT="$2"; shift 2 ;;
    --compose-file) COMPOSE_FILE="$2"; shift 2 ;;
    --project) PROJECT="$2"; shift 2 ;;
    --container) CONTAINER="$2"; shift 2 ;;
    --app-image) APP_IMAGE="$2"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

COMPOSE_FILE="$(host_path "$COMPOSE_FILE")"

if [ -n "$CONTAINER" ]; then
  PGC="$CONTAINER"
else
  PGC="$(service_container "$COMPOSE_FILE" "$PROJECT" postgres)"
fi
info "postgres container: $PGC"
info "backup root: $BACKUP_ROOT"

if ! docker exec -u postgres "$PGC" pg_isready -d "${POSTGRES_DB:-shop_mall}" >/dev/null 2>&1; then
  die "postgres at $PGC is not ready"
fi

if [ "${APP_IMAGE:-unknown}" = "unknown" ]; then
  APP_IMAGE="$(compose_app_image "$COMPOSE_FILE" "$PROJECT")"
  APP_IMAGE="${APP_IMAGE:-unknown}"
fi

if EPOCH="$(force_wal_advance_epoch "$PGC" "${POSTGRES_DB:-shop_mall}" "$WAIT_ARCHIVE_SECONDS")"; then
  write_wal_marker "$BACKUP_ROOT/state" "$EPOCH" "$APP_IMAGE"
  AGE="$(( $(date +%s) - EPOCH ))"
  info "WAL marker refreshed (epoch=$EPOCH, age=${AGE}s, app_version=$APP_IMAGE)"
  echo "WAL_MARKER_EPOCH=$EPOCH"
  echo "WAL_MARKER_AGE_SECONDS=$AGE"
else
  die "archiver did not advance within ${WAIT_ARCHIVE_SECONDS}s; WAL freshness NOT refreshed (archive_command may be broken)"
fi