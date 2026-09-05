#!/usr/bin/env bash
# preflight.sh — release gate checks before a deployment:
#   1. image tag immutability (no latest/dev/test/staging)
#   2. compose config validity
#   3. migration version matches the expected forward version
#   4. backup freshness (full DB backup <= 24h, WAL archive <= 1h)
#   5. disk space for a backup + new image
#
# Exits 0 when every check passes, non-zero otherwise (CI-friendly).
# Checks that need the live database degrade to a warning when the postgres
# service is not running.
set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../backup/lib.sh
source "$SCRIPT_DIR/../backup/lib.sh"

require_cmd docker jq

COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose/docker-compose.yml}"
PROJECT="${PROJECT:-}"
BACKUP_ROOT="${BACKUP_ROOT:-/var/backups/shop-mall}"
EXPECTED_MIGRATION_VERSION="${EXPECTED_MIGRATION_VERSION:-}"
FULL_BACKUP_MAX_AGE="${FULL_BACKUP_MAX_AGE:-86400}"   # 24h
WAL_MAX_AGE="${WAL_MAX_AGE:-3600}"                     # 1h
MIN_FREE_KB="${MIN_FREE_KB:-1048576}"                  # 1 GiB

# Git Bash: docker needs Windows-style paths for host files.
COMPOSE_FILE="$(host_path "$COMPOSE_FILE")"

FAIL=0
PASS() { echo "PASS  $*"; }
FAILX() { echo "FAIL  $*"; FAIL=1; }
WARNX() { echo "WARN  $*"; }

echo "---- preflight $(now_iso_utc) ----"

# 1. image tag immutability -------------------------------------------------
APP_IMAGE="$(compose_app_image "$COMPOSE_FILE" "$PROJECT")"
if [ -z "$APP_IMAGE" ]; then
  APP_IMAGE="${APP_IMAGE_ENV:-}"
fi
if [ -z "$APP_IMAGE" ]; then
  WARNX "app image tag not resolvable from compose (APP_IMAGE empty); skipping immutability check"
elif is_immutable_tag "$APP_IMAGE"; then
  PASS "app image tag is immutable: $APP_IMAGE"
else
  FAILX "app image tag is a rolling/mutable tag: $APP_IMAGE (use an immutable date/hash tag)"
fi

# 2. compose config validity -------------------------------------------------
if docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} config >/dev/null 2>&1; then
  PASS "compose config parses"
else
  FAILX "compose config failed to parse ($COMPOSE_FILE)"
fi

# 3. migration version -------------------------------------------------------
if [ -z "$EXPECTED_MIGRATION_VERSION" ]; then
  EXPECTED_MIGRATION_VERSION="$(
    ls -1 "$REPO_ROOT"/backend/migrations/*.up.sql 2>/dev/null \
      | sed -E 's#.*/([0-9]+)_.*#\1#' | sort -n | tail -1
  )"
fi
if [ -z "$EXPECTED_MIGRATION_VERSION" ]; then
  FAILX "could not derive the expected migration version from backend/migrations"
else
  if docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} ps -q postgres >/dev/null 2>&1; then
    PGC="$(service_container "$COMPOSE_FILE" "$PROJECT" postgres 2>/dev/null || true)"
    if [ -n "$PGC" ]; then
      ACTUAL="$(migration_version "$PGC" shop_mall 2>/dev/null || echo "")"
      if [ "$ACTUAL" = "$EXPECTED_MIGRATION_VERSION" ]; then
        PASS "migration version matches expected: $ACTUAL"
      else
        FAILX "migration version $ACTUAL != expected $EXPECTED_MIGRATION_VERSION"
      fi
    else
      WARNX "postgres service not running; cannot verify migration version"
    fi
  else
    WARNX "postgres service not running; cannot verify migration version"
  fi
fi

# 4. backup freshness ---------------------------------------------------------
DB_AGE="$(marker_age_seconds "$BACKUP_ROOT/state/last-db.marker" || true)"
if [ -n "$DB_AGE" ]; then
  if [ "$DB_AGE" -le "$FULL_BACKUP_MAX_AGE" ]; then
    PASS "full db backup age ${DB_AGE}s <= ${FULL_BACKUP_MAX_AGE}s"
  else
    FAILX "full db backup age ${DB_AGE}s > ${FULL_BACKUP_MAX_AGE}s"
  fi
else
  FAILX "no full db backup marker at $BACKUP_ROOT/state/last-db.marker"
fi

WAL_AGE="$(marker_age_seconds "$BACKUP_ROOT/state/last-wal.marker" || true)"
if [ -n "$WAL_AGE" ]; then
  if [ "$WAL_AGE" -le "$WAL_MAX_AGE" ]; then
    PASS "wal archive age ${WAL_AGE}s <= ${WAL_MAX_AGE}s"
  else
    FAILX "wal archive age ${WAL_AGE}s > ${WAL_MAX_AGE}s"
  fi
else
  FAILX "no wal archive marker at $BACKUP_ROOT/state/last-wal.marker"
fi

IMG_AGE="$(marker_age_seconds "$BACKUP_ROOT/state/last-images.marker" || true)"
if [ -n "$IMG_AGE" ] && [ "$IMG_AGE" -le "$FULL_BACKUP_MAX_AGE" ]; then
  PASS "images backup age ${IMG_AGE}s <= ${FULL_BACKUP_MAX_AGE}s"
else
  WARNX "images backup marker missing or older than ${FULL_BACKUP_MAX_AGE}s"
fi

# 5. disk space ----------------------------------------------------------------
if [ -d "$BACKUP_ROOT" ]; then
  FREE_KB="$(df -k "$BACKUP_ROOT" | awk 'NR==2 {print $4}')"
  if [ -n "$FREE_KB" ] && [ "$FREE_KB" -ge "$MIN_FREE_KB" ]; then
    PASS "disk free at $BACKUP_ROOT: $(( FREE_KB / 1024 )) MiB"
  else
    FAILX "disk free at $BACKUP_ROOT: ${FREE_KB:-0} KiB < ${MIN_FREE_KB} KiB"
  fi
else
  WARNX "backup root $BACKUP_ROOT does not exist; disk check skipped"
fi

echo "-----------------------------------"
if [ "$FAIL" = 1 ]; then
  echo "PREFLIGHT RESULT: FAIL"
  exit 1
fi
echo "PREFLIGHT RESULT: PASS"
exit 0