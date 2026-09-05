#!/usr/bin/env bash
# backup-db.sh — daily full PostgreSQL backup plus an at-least-hourly WAL
# archive policy.
#
# Produces, into <BACKUP_ROOT>:
#   db/<id>.dump          logical full backup (pg_dump -Fc), optionally encrypted
#   base/<id>.tar.gz      base backup (pg_basebackup) for WAL point-in-time
#                         recovery, only when the postgres container exposes a
#                         /backup mount (drill) or /var/backups/base (the
#                         docker-compose.wal.yml production override)
#   state/last-db.marker  freshness marker for preflight / alerting
#   state/last-wal.marker freshness marker for the WAL archive policy
#   state/backup_last_success.prom  Prometheus textfile for node_exporter
#
# WAL archive policy: the postgres server must run with archive_mode=on and an
# archive_command writing into a persistent volume (see docker-compose.wal.yml).
# This script forces a segment switch and confirms the archiver copied it, then
# prints the hourly cron line that bounds RPO to <= 1h on low-volume servers.
#
# Never touches the application configuration or the live container's data
# volume beyond the documented read-only archive work.
set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

require_cmd docker jq

# --- configuration ---------------------------------------------------------

BACKUP_ROOT="${BACKUP_ROOT:-/var/backups/shop-mall}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose/docker-compose.yml}"
PROJECT="${PROJECT:-}"
POSTGRES_DB="${POSTGRES_DB:-shop_mall}"
POSTGRES_USER="${POSTGRES_USER:-shop_mall}"
APP_IMAGE="${APP_IMAGE:-unknown}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-30}"
WAL_MAX_AGE_SECONDS="${WAL_MAX_AGE_SECONDS:-3600}"
# Drill mode: --container NAME runs the backup inside a named container that has
# the backup store mounted at /backup (the composable drill setup).
CONTAINER=""
WAIT_ARCHIVE_SECONDS="${WAIT_ARCHIVE_SECONDS:-20}"

usage() {
  cat <<'EOF' >&2
usage: backup-db.sh [options]

options:
  --backup-root DIR     backup store path (default: /var/backups/shop-mall)
  --compose-file FILE   compose file defining the postgres service
  --project NAME        compose project name (default: derived from compose file)
  --container NAME      run against a named postgres container with /backup mounted
                        (drill mode; BACKUP_ROOT is then the in-container path)
  --help                show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --backup-root) BACKUP_ROOT="$2"; shift 2 ;;
    --compose-file) COMPOSE_FILE="$2"; shift 2 ;;
    --project) PROJECT="$2"; shift 2 ;;
    --container) CONTAINER="$2"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

# Git Bash: docker needs Windows-style paths for host files.
COMPOSE_FILE="$(host_path "$COMPOSE_FILE")"

# --- resolve the postgres container ---------------------------------------

if [ -n "$CONTAINER" ]; then
  PGC="$CONTAINER"
  IN_CONTAINER_BACKUP=1
  # In drill mode the store is the /backup mount inside the container.
  ROOT="$BACKUP_ROOT"
else
  PGC="$(service_container "$COMPOSE_FILE" "$PROJECT" postgres)"
  IN_CONTAINER_BACKUP=0
  ROOT="$BACKUP_ROOT"
fi

info "postgres container: $PGC"
info "backup root: $BACKUP_ROOT"

# Verify the database answers before doing any work.
if ! docker exec -u postgres "$PGC" pg_isready -d "$POSTGRES_DB" >/dev/null 2>&1; then
  die "postgres at $PGC is not ready"
fi

# --- versions --------------------------------------------------------------

MIGRATION_VERSION="$(migration_version "$PGC" "$POSTGRES_DB")"
APP_VERSION="$APP_IMAGE"
[ "$APP_VERSION" = "unknown" ] && APP_VERSION="$(compose_app_image "$COMPOSE_FILE" "$PROJECT")"
APP_VERSION="${APP_VERSION:-unknown}"

# --- backup id -------------------------------------------------------------

ID="db-$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_TIME="$(now_iso_utc)"
mkdir -p "$ROOT/db" "$ROOT/base" "$ROOT/state"

info "backup id: $ID"
info "migration version: $MIGRATION_VERSION app version: $APP_VERSION"

# --- 1. logical full dump --------------------------------------------------

DUMP_IN_PATH="$ROOT/db/$ID.dump"
if [ "$IN_CONTAINER_BACKUP" = 1 ]; then
  docker exec -u postgres "$PGC" pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -f "/backup/db/$ID.dump"
else
  docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} exec -T postgres \
    pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB" > "$DUMP_IN_PATH"
fi

# --- 2. base backup for WAL point-in-time recovery -------------------------

BASE_BACKUP=""
if [ "$IN_CONTAINER_BACKUP" = 1 ]; then
  BASE_BACKUP="/backup/base/$ID"
  docker exec -u postgres "$PGC" sh -c \
    "pg_basebackup -h /var/run/postgresql -U '$POSTGRES_USER' -D '$BASE_BACKUP' -Fp -X stream"
  docker exec -u postgres "$PGC" sh -c \
    "tar -czf '$BASE_BACKUP.tar.gz' -C '$BASE_BACKUP' . && find '$BASE_BACKUP' -depth -delete"
  BASE_BACKUP="base/$ID.tar.gz"
elif docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} exec -T postgres \
  sh -c 'test -d /var/backups/base' >/dev/null 2>&1; then
  # Production override (docker-compose.wal.yml) exposes /var/backups/base.
  docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} exec -T postgres sh -c \
    "pg_basebackup -h /var/run/postgresql -U '$POSTGRES_USER' -D /var/backups/base/$ID -Fp -X stream"
  docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} exec -T postgres sh -c \
    "tar -czf /var/backups/base/$ID.tar.gz -C /var/backups/base/$ID . && find /var/backups/base/$ID -depth -delete"
  docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} cp \
    "postgres:/var/backups/base/$ID.tar.gz" "$ROOT/base/$ID.tar.gz"
  BASE_BACKUP="base/$ID.tar.gz"
else
  warn "no /backup or /var/backups/base mount in the postgres container; skipping base backup"
  warn "WAL point-in-time recovery requires the docker-compose.wal.yml override"
fi

BASE_CHECKSUM=""
if [ -n "$BASE_BACKUP" ]; then
  if [ "$IN_CONTAINER_BACKUP" = 1 ]; then
    BASE_CHECKSUM="$(docker exec -u postgres "$PGC" sha256sum "/backup/$BASE_BACKUP" | awk '{print $1}')"
  else
    BASE_CHECKSUM="$(sha256_file "$ROOT/$BASE_BACKUP")"
  fi
fi

# --- 3. WAL archive policy -------------------------------------------------

# Force a segment switch and confirm the archiver copied it. This both bounds
# RPO (each archived segment is a durable restore point) and lets the operator
# measure archive delay. On very low write volume the switch can be a no-op, so
# a WAL record is emitted to guarantee rotation (shared helper in lib.sh,
# also used by the hourly touch-wal-marker.sh).
WAL_ARCHIVING=""
if ARCHIVE_EPOCH="$(force_wal_advance_epoch "$PGC" "$POSTGRES_DB" "$WAIT_ARCHIVE_SECONDS")"; then
  WAL_ARCHIVING="on"
  write_wal_marker "$ROOT/state" "$ARCHIVE_EPOCH" "$APP_VERSION"
  WAL_AGE="$(( $(date +%s) - ARCHIVE_EPOCH ))"
  info "WAL archiving confirmed (last archived $WAL_AGE seconds ago)"
  cat <<'EOF' >&2
WAL archive policy (at least hourly): install this cron line on the host so the
WAL freshness marker stays current (preflight gate) AND a segment rotates at
least once an hour (RPO bound on low-write servers). touch-wal-marker.sh does
the switch, waits for the archiver, and refreshes state/last-wal.marker:

  0 * * * * cd /path/to/shop-mall && bash deploy/backup/touch-wal-marker.sh --backup-root /var/backups/shop-mall --compose-file deploy/compose/docker-compose.yml >>/var/log/shop-mall-wal-touch.log 2>&1
EOF
else
  WAL_ARCHIVING="off"
  warn "archiver did not advance within ${WAIT_ARCHIVE_SECONDS}s; archive_command may not be enabled"
  warn "WAL-based RPO is NOT guaranteed until docker-compose.wal.yml is deployed"
fi

# --- 4. snapshot statistics for metadata ----------------------------------

STATS="$(psql_exec "$PGC" "$POSTGRES_DB" \
  "SELECT (SELECT count(*) FROM users)||'|'||(SELECT count(*) FROM orders)||'|'||(SELECT count(*) FROM order_items)||'|'||(SELECT count(*) FROM products)||'|'||(SELECT COALESCE(sum(points_balance),0) FROM users);")"
USERS_N="${STATS%%|*}"; STATS="${STATS#*|}"
ORDERS_N="${STATS%%|*}"; STATS="${STATS#*|}"
ORDER_ITEMS_N="${STATS%%|*}"; STATS="${STATS#*|}"
PRODUCTS_N="${STATS%%|*}"; STATS="${STATS#*|}"
POINTS_SUM="$STATS"

# --- 5. checksum + encryption ---------------------------------------------

ARTIFACT_PATH="$DUMP_IN_PATH"
ARTIFACT_EXT="dump"
if encrypt_enabled; then
  ENC_KEY_VERSION="$(encryption_key_version)"
  encrypt_file "$DUMP_IN_PATH" "$DUMP_IN_PATH.enc"
  find "$DUMP_IN_PATH" -depth -delete
  ARTIFACT_PATH="$DUMP_IN_PATH.enc"
  ARTIFACT_EXT="dump.enc"
else
  ENC_KEY_VERSION="unencrypted"
fi
CHECKSUM="$(sha256_file "$ARTIFACT_PATH")"

# --- 6. metadata + markers ------------------------------------------------

UPLOAD_LOCATION="${BACKUP_UPLOAD_TARGET:-$BACKUP_ROOT}"
write_backup_marker "$ROOT/state" db "$(now_epoch)" \
  "backup_id=$ID" \
  "kind=postgres-full" \
  "backup_time=$BACKUP_TIME" \
  "app_version=$APP_VERSION" \
  "migration_version=$MIGRATION_VERSION" \
  "checksum_algorithm=sha256" \
  "checksum=$CHECKSUM" \
  "encryption_key_version=$ENC_KEY_VERSION" \
  "upload_location=$UPLOAD_LOCATION" \
  "artifact=$ID.$ARTIFACT_EXT" \
  "base_backup=$BASE_BACKUP" \
  "base_checksum=$BASE_CHECKSUM" \
  "wal_archiving=$WAL_ARCHIVING" \
  "wal_archived_until=${WAL_ARCHIVED_UNTIL:-}" \
  "users_count=$USERS_N" \
  "orders_count=$ORDERS_N" \
  "order_items_count=$ORDER_ITEMS_N" \
  "products_count=$PRODUCTS_N" \
  "points_sum=$POINTS_SUM" \
  "size_bytes=$(stat -c %s "$ARTIFACT_PATH" 2>/dev/null || wc -c < "$ARTIFACT_PATH")"

# --- 7. retention ----------------------------------------------------------

if [ "$IN_CONTAINER_BACKUP" = 1 ]; then
  info "retention pruning is skipped in container mode (drill); the store is ephemeral"
else
  find "$ROOT/db" -maxdepth 1 -type f -name 'db-*.dump*' -mtime "+$BACKUP_RETENTION_DAYS" -delete 2>/dev/null || true
  find "$ROOT/base" -maxdepth 1 -type f -name 'db-*.tar.gz' -mtime "+$BACKUP_RETENTION_DAYS" -delete 2>/dev/null || true
  info "pruned backups older than ${BACKUP_RETENTION_DAYS} days"
fi

# --- summary ---------------------------------------------------------------

info "backup complete: $ARTIFACT_PATH"
info "  migration_version=$MIGRATION_VERSION app_version=$APP_VERSION checksum=$CHECKSUM"
info "  users=$USERS_N orders=$ORDERS_N order_items=$ORDER_ITEMS_N products=$PRODUCTS_N points_sum=$POINTS_SUM"
info "  wal_archiving=$WAL_ARCHIVING encryption=$ENC_KEY_VERSION upload_location=$UPLOAD_LOCATION"
echo "BACKUP_ID=$ID"
echo "BACKUP_ARTIFACT=$ARTIFACT_PATH"
echo "BACKUP_CHECKSUM=$CHECKSUM"
echo "BACKUP_MIGRATION_VERSION=$MIGRATION_VERSION"
echo "BACKUP_WAL_ARCHIVING=$WAL_ARCHIVING"