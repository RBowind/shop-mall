#!/usr/bin/env bash
# restore.sh — recreate PostgreSQL (base backup + WAL replay to a target
# restore point), the image volume and the configuration references in an
# ISOLATED docker project, then validate the restore BEFORE any traffic
# cutover. It never touches the live project's containers or volumes.
#
# Restore point:
#   --restore-point latest      replay every archived WAL segment (default)
#   --restore-point <lsn>       stop at a WAL LSN (e.g. 0/16B2E70)
#   --restore-point <time>      stop at a transaction commit time (ISO UTC)
#
# Validation performed on the restored instance:
#   - migration version matches the backup metadata
#   - users.points_balance equals the sum of points_ledger.delta per user
#   - users.points_balance equals the last ledger balance_after per user
#   - orders.total_points equals the order_items price snapshot sum
#   - every order has at least one order_item (order snapshots preserved)
#   - object keys referenced by products/order_items exist in the restored
#     image volume; referenced-but-missing keys are reported as an ALERT and
#     are never deleted (restore must not GC order snapshots)
#   - optional --expect-* count assertions (used by the drill)
#
# Output: a JSON summary on stdout plus, when RESTORE_REPORT is set, a copy of
# it written to that path.
set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

require_cmd docker jq

BACKUP_ROOT="${BACKUP_ROOT:-/var/backups/shop-mall}"
PROJECT="${PROJECT:-shop-mall-restore}"
RESTORE_POINT="${RESTORE_POINT:-latest}"
WAL_VOLUME="${WAL_VOLUME:-${PROJECT}_backup-wal}"
IMAGES_VOLUME="${IMAGES_VOLUME:-${PROJECT}_images}"
NETWORK="${NETWORK:-${PROJECT}_restore}"
POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:16-alpine}"
DB_NAME="${POSTGRES_DB:-shop_mall}"
DB_USER="${POSTGRES_USER:-shop_mall}"
RESTORE_CONTAINER="${RESTORE_CONTAINER:-${PROJECT}-restore-db}"
RESTORE_REPORT="${RESTORE_REPORT:-}"
EXPECT_ORDERS=""
EXPECT_USERS=""
EXPECT_PRODUCTS=""
SKIP_IMAGES=0
# The image-volume .snar machinery needs GNU tar (BusyBox tar has no
# --listed-incremental), so both extraction and the validation listing use a
# GNU-tar-capable image; default debian:stable-slim, override with TAR_IMAGE.
TAR_IMAGE="${TAR_IMAGE:-debian:stable-slim}"

usage() {
  cat <<'EOF' >&2
usage: restore.sh [options]

  --backup-root DIR      backup store (default: /var/backups/shop-mall)
  --project NAME         isolated docker project name for the restored instance
  --restore-point P      latest | <LSN> | <ISO time>   (default: latest)
  --wal-volume NAME      docker volume holding the archived WAL (mounted /wal)
  --images-volume NAME   docker volume to restore images into (default <project>_images)
  --network NAME         docker network the restored instance joins
  --restore-container NAME  container name for the restored postgres
  --tar-image IMAGE      GNU-tar-capable image for the image-volume chain
                         restore + validation listing (default debian:stable-slim)
  --expect-orders N      assert restored order count equals N
  --expect-users N       assert restored user count equals N
  --expect-products N    assert restored product count equals N
  --no-images            skip the image volume restore + object-key validation
  --help                 show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --backup-root) BACKUP_ROOT="$2"; shift 2 ;;
    --project) PROJECT="$2"; shift 2 ;;
    --restore-point) RESTORE_POINT="$2"; shift 2 ;;
    --wal-volume) WAL_VOLUME="$2"; shift 2 ;;
    --images-volume) IMAGES_VOLUME="$2"; shift 2 ;;
    --network) NETWORK="$2"; shift 2 ;;
    --restore-container) RESTORE_CONTAINER="$2"; shift 2 ;;
    --postgres-image) POSTGRES_IMAGE="$2"; shift 2 ;;
    --tar-image) TAR_IMAGE="$2"; shift 2 ;;
    --expect-orders) EXPECT_ORDERS="$2"; shift 2 ;;
    --expect-users) EXPECT_USERS="$2"; shift 2 ;;
    --expect-products) EXPECT_PRODUCTS="$2"; shift 2 ;;
    --no-images) SKIP_IMAGES=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

START_NS="$(now_ns)"
info "restore started for project '$PROJECT'"

# --- resolve the latest db backup -----------------------------------------

# Metadata (with the marker) lives under state/; the artifacts under db/ and
# base/. Both are written by backup-db.sh.
DB_META="$(ls -1t "$BACKUP_ROOT"/state/db-*.json 2>/dev/null | head -1)"
[ -n "$DB_META" ] || die "no db backup metadata found under $BACKUP_ROOT/state"
DB_ID="$(jq_file '.backup_id' "$DB_META")"
ARTIFACT_NAME="$(jq_file '.artifact' "$DB_META")"
MIG_VERSION="$(jq_file '.migration_version' "$DB_META")"
DB_CHECKSUM="$(jq_file '.checksum' "$DB_META")"
ENC_VERSION="$(jq_file '.encryption_key_version' "$DB_META")"
BASE_BACKUP="$(jq_file '.base_backup' "$DB_META")"
BASE_CHECKSUM="$(jq_file '.base_checksum // ""' "$DB_META")"
DB_ORDERS_AT_BACKUP="$(jq_file '.orders_count // ""' "$DB_META")"

[ -n "$BASE_BACKUP" ] && [ "$BASE_BACKUP" != "null" ] || \
  die "backup $DB_ID has no base backup; WAL point-in-time restore is impossible"

info "backup id: $DB_ID  migration version: $MIG_VERSION  restore point: $RESTORE_POINT"

# --- extract / verify / decrypt the db artifact ---------------------------

TMP="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP" 2>/dev/null || true
}
trap cleanup EXIT

ARTIFACT="$BACKUP_ROOT/db/$ARTIFACT_NAME"
[ -f "$ARTIFACT" ] || die "db artifact missing: $ARTIFACT"
if [ "$(sha256_file "$ARTIFACT")" != "$DB_CHECKSUM" ]; then
  die "checksum mismatch on $ARTIFACT (expected $DB_CHECKSUM)"
fi
info "db artifact checksum ok: $ARTIFACT"

BASE_TAR="$BACKUP_ROOT/$BASE_BACKUP"
[ -f "$BASE_TAR" ] || die "base backup missing: $BASE_TAR"
if [ -n "$BASE_CHECKSUM" ]; then
  if [ "$(sha256_file "$BASE_TAR")" != "$BASE_CHECKSUM" ]; then
    die "checksum mismatch on $BASE_TAR (expected $BASE_CHECKSUM)"
  fi
  info "base backup checksum ok: $BASE_TAR"
fi

if [ "$ENC_VERSION" != "unencrypted" ]; then
  decrypt_file "$ARTIFACT" "$TMP/restore.dump"
  info "db artifact decrypted (key version $ENC_VERSION)"
  ARTIFACT="$TMP/restore.dump"
fi

# --- images backup resolution ----------------------------------------------

images_artifact_for() {
  # images_artifact_for <meta.json> -> prints the on-disk artifact path
  local m="$1" f
  f="$BACKUP_ROOT/images/$(jq_file '.backup_id' "$m").tar.gz"
  [ -f "$f" ] || f="$f.enc"
  [ -f "$f" ] || return 1
  printf '%s\n' "$f"
}

# --- prepare the isolated postgres data volume -----------------------------

DATA_VOLUME="${PROJECT}_postgres-data"
docker volume create "$DATA_VOLUME" >/dev/null
[ -d "/var/lib/postgresql/data" ] || true

# Recovery configuration is written next to the base backup. A WAL base backup
# is restored into a fresh data dir; recovery.signal + restore_command make the
# server replay the archived WAL up to the target, then promote.
RECOVERY_CONF="$(mktemp)"
{
  printf 'restore_command = '\''cp /wal/%%f %%p'\''\n'
  printf 'recovery_target_timeline = '\''latest'\''\n'
  printf 'archive_mode = off\n'
  if [ "$RESTORE_POINT" != "latest" ]; then
    if printf '%s' "$RESTORE_POINT" | grep -Eq '^[0-9A-Fa-f]+/[0-9A-Fa-f]+$'; then
      printf "recovery_target_lsn = '%s'\n" "$RESTORE_POINT"
    else
      printf "recovery_target_time = '%s'\n" "$RESTORE_POINT"
    fi
    printf "recovery_target_action = 'promote'\n"
  fi
} > "$RECOVERY_CONF"

info "populating $DATA_VOLUME from base backup (restore point $RESTORE_POINT)"
docker run --rm \
  -v "$DATA_VOLUME":/var/lib/postgresql/data \
  -v "$(host_path "$BASE_TAR")":/backup/base.tar.gz:ro \
  -v "$(host_path "$RECOVERY_CONF")":/recovery.conf:ro \
  -v "$WAL_VOLUME":/wal:ro \
  "$POSTGRES_IMAGE" sh -c '
    mkdir -p /var/lib/postgresql/data
    find /var/lib/postgresql/data -depth -delete
    tar -xzf /backup/base.tar.gz -C /var/lib/postgresql/data
    chown -R postgres:postgres /var/lib/postgresql/data
    touch /var/lib/postgresql/data/recovery.signal
    cat /recovery.conf >> /var/lib/postgresql/data/postgresql.auto.conf
  '

# --- start the restored postgres -------------------------------------------

docker rm -f "$RESTORE_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$RESTORE_CONTAINER" \
  --network "$NETWORK" \
  -e POSTGRES_HOST_AUTH_METHOD=trust \
  -v "$DATA_VOLUME":/var/lib/postgresql/data \
  -v "$WAL_VOLUME":/wal:ro \
  "$POSTGRES_IMAGE" >/dev/null
info "restored postgres container: $RESTORE_CONTAINER"

wait_restore_ready() {
  local c="$1" n=0
  until docker exec -u postgres "$c" pg_isready >/dev/null 2>&1; do
    n=$((n + 1)); [ "$n" -gt 90 ] && return 1; sleep 2
  done
  n=0
  until [ "$(docker exec -u postgres "$c" psql -d "$DB_NAME" -U "$DB_USER" -At -c 'SELECT pg_is_in_recovery();' 2>/dev/null)" = "f" ]; do
    n=$((n + 1))
    if [ "$n" -gt 90 ]; then
      info "recovery target pending; promoting explicitly"
      docker exec -u postgres "$c" psql -d "$DB_NAME" -U "$DB_USER" -c "SELECT pg_promote();" >/dev/null 2>&1 || true
      n=0
      until [ "$(docker exec -u postgres "$c" psql -d "$DB_NAME" -U "$DB_USER" -At -c 'SELECT pg_is_in_recovery();' 2>/dev/null)" = "f" ]; do
        n=$((n + 1)); [ "$n" -gt 90 ] && return 1; sleep 2
      done
      break
    fi
    sleep 2
  done
  docker exec -u postgres "$c" psql -d "$DB_NAME" -U "$DB_USER" -At -c 'SELECT 1;' >/dev/null
}

if ! wait_restore_ready "$RESTORE_CONTAINER"; then
  die "restored postgres did not become ready within the timeout"
fi
info "restored postgres is ready (recovery complete, promoted)"

# --- restore the image volume ----------------------------------------------

MISSING_KEYS=""
ORPHAN_KEYS=""
if [ "$SKIP_IMAGES" = 1 ]; then
  warn "image volume restore skipped (--no-images)"
else
  docker volume create "$IMAGES_VOLUME" >/dev/null
  # Start the replay from a clean slate so the chain is authoritative even if the
  # target volume was populated by an interrupted earlier run (the GNU tar
  # listed-incremental extraction can only remove files the snapshot knows about;
  # residue not in any snapshot would otherwise survive).
  docker run --rm -v "$IMAGES_VOLUME":/data "$TAR_IMAGE" \
    sh -c 'find /data -mindepth 1 -delete' >/dev/null 2>&1 || true
  apply_images_backups() {
    local metas m mode art
    # backup-images.sh names its metadata state/images-img-<id>.json, so match
    # on images-* (a plain img-* glob would silently find nothing).
    metas="$(ls -1 "$BACKUP_ROOT"/state/images-*.json 2>/dev/null | sort || true)"
    [ -n "$metas" ] || { warn "no image backups found; object-key validation limited to the existing volume"; return 0; }
    # Replay the last full backup first, then every later backup in order.
    local pending=""
    for m in $metas; do
      mode="$(jq_file '.mode' "$m")"
      if [ "$mode" = "full" ]; then
        pending="$m"
      else
        pending="$pending $m"
      fi
    done
    for m in $pending; do
      art="$(images_artifact_for "$m" 2>/dev/null || true)"
      [ -n "$art" ] || continue
      local id mode
      id="$(jq_file '.backup_id' "$m")"
      mode="$(jq_file '.mode' "$m")"
      if [ "$mode" = "incremental" ]; then
        # GNU tar listed-incremental extraction with the archived post-state
        # snapshot applies additions AND deletions since the previous snapshot.
        docker run --rm -v "$IMAGES_VOLUME":/data \
          -v "$(host_path "$BACKUP_ROOT/images")":/backup:ro "$TAR_IMAGE" \
          tar -xzf "/backup/$id.tar.gz" --listed-incremental="/backup/snar/$id.snar" -C /data
      else
        docker run --rm -v "$IMAGES_VOLUME":/data \
          -v "$(host_path "$BACKUP_ROOT/images")":/backup:ro "$TAR_IMAGE" \
          tar -xzf "/backup/$id.tar.gz" -C /data
      fi
      info "images backup applied: $id ($mode)"
    done
  }
  apply_images_backups
fi

# --- validation ------------------------------------------------------------

RESULT_OK=1
ASSERT_COUNT() {  # ASSERT_COUNT <label> <actual> <expected>
  if [ "$2" = "$3" ]; then
    echo "PASS  $1=$2"
  else
    echo "FAIL  $1=$2  expected=$3"
    RESULT_OK=0
  fi
}

info "running restore validation"
echo "---- restore validation ----"

VER="$(migration_version "$RESTORE_CONTAINER" "$DB_NAME")"
ASSERT_COUNT "migration_version" "$VER" "$MIG_VERSION"

LEDGER_MISMATCH="$(psql_exec "$RESTORE_CONTAINER" "$DB_NAME" \
  "SELECT count(*) FROM (SELECT u.id FROM users u LEFT JOIN points_ledger l ON l.user_id = u.id GROUP BY u.id, u.points_balance HAVING u.points_balance <> COALESCE(SUM(l.delta), 0)) x;")"
ASSERT_COUNT "points_balance_vs_ledger_sum_mismatches" "$LEDGER_MISMATCH" "0"

LAST_BAL_MISMATCH="$(psql_exec "$RESTORE_CONTAINER" "$DB_NAME" \
  "SELECT count(*) FROM users u LEFT JOIN LATERAL (SELECT balance_after FROM points_ledger l WHERE l.user_id = u.id ORDER BY l.id DESC LIMIT 1) last ON true WHERE u.points_balance <> COALESCE(last.balance_after, 0);")"
ASSERT_COUNT "points_balance_vs_last_balance_after_mismatches" "$LAST_BAL_MISMATCH" "0"

ORDER_TOTAL_MISMATCH="$(psql_exec "$RESTORE_CONTAINER" "$DB_NAME" \
  "SELECT count(*) FROM orders o LEFT JOIN (SELECT order_id, COALESCE(SUM(price_snapshot * quantity), 0) AS s FROM order_items GROUP BY order_id) i ON i.order_id = o.id WHERE o.total_points <> COALESCE(i.s, 0);")"
ASSERT_COUNT "order_total_vs_items_mismatches" "$ORDER_TOTAL_MISMATCH" "0"

EMPTY_ORDERS="$(psql_exec "$RESTORE_CONTAINER" "$DB_NAME" \
  "SELECT count(*) FROM orders o WHERE NOT EXISTS (SELECT 1 FROM order_items i WHERE i.order_id = o.id);")"
ASSERT_COUNT "orders_without_items" "$EMPTY_ORDERS" "0"

if [ -n "$DB_ORDERS_AT_BACKUP" ]; then
  echo "NOTE  orders_at_backup=$DB_ORDERS_AT_BACKUP (WAL replay may add more)"
fi

if [ -n "$EXPECT_ORDERS" ]; then
  ACT="$(psql_exec "$RESTORE_CONTAINER" "$DB_NAME" "SELECT count(*) FROM orders;")"
  ASSERT_COUNT "orders" "$ACT" "$EXPECT_ORDERS"
fi
if [ -n "$EXPECT_USERS" ]; then
  ACT="$(psql_exec "$RESTORE_CONTAINER" "$DB_NAME" "SELECT count(*) FROM users;")"
  ASSERT_COUNT "users" "$ACT" "$EXPECT_USERS"
fi
if [ -n "$EXPECT_PRODUCTS" ]; then
  ACT="$(psql_exec "$RESTORE_CONTAINER" "$DB_NAME" "SELECT count(*) FROM products;")"
  ASSERT_COUNT "products" "$ACT" "$EXPECT_PRODUCTS"
fi

# object-key validation against the restored image volume
if [ "$SKIP_IMAGES" = 0 ]; then
  DB_KEYS="$(mktemp)"
  VOL_KEYS="$(mktemp)"
  # Strip the /data prefix with sed instead of find -printf so it also works on
  # BusyBox find; sort -u keeps the set comparison stable.
  docker run --rm -v "$IMAGES_VOLUME":/data:ro "$TAR_IMAGE" \
    find /data -type f 2>/dev/null | sed 's#^/data/##' | LC_ALL=C sort -u > "$VOL_KEYS" || true
  psql_exec "$RESTORE_CONTAINER" "$DB_NAME" \
    "SELECT DISTINCT k FROM (SELECT main_image AS k FROM products WHERE btrim(main_image) <> '' UNION SELECT product_image AS k FROM order_items WHERE btrim(product_image) <> '') t;" \
    2>/dev/null | sed 's/[[:space:]]*$//' | LC_ALL=C sort -u > "$DB_KEYS"
  MISSING_KEYS="$(comm -23 "$DB_KEYS" "$VOL_KEYS")"
  ORPHAN_KEYS="$(comm -13 "$DB_KEYS" "$VOL_KEYS")"
  PRESENT_KEYS="$(comm -12 "$DB_KEYS" "$VOL_KEYS")"
  PRESENT_N="$(printf '%s' "$PRESENT_KEYS" | grep -c . 2>/dev/null || true)"
  echo "PASS  object_keys_present=$PRESENT_N"
  if [ -n "$MISSING_KEYS" ]; then
    echo "ALERT referenced_but_missing_image_keys (alert only, NOT deleted):"
    printf '%s\n' "$MISSING_KEYS" | sed 's/^/        /'
  else
    echo "PASS  no referenced-but-missing image keys"
  fi
  if [ -n "$ORPHAN_KEYS" ]; then
    echo "NOTE  orphan_image_keys (subject to grace-period cleanup, not touched by restore):"
    printf '%s\n' "$ORPHAN_KEYS" | sed 's/^/        /'
  fi
  rm -f "$DB_KEYS" "$VOL_KEYS"
fi

echo "-----------------------------------"

READY_NS="$(now_ns)"
RTO_SECONDS="$(awk -v a="$READY_NS" -v b="$START_NS" 'BEGIN{printf "%.3f", a-b}')"

RESULT='{"restore_project": "'$PROJECT'", "restore_point": "'$RESTORE_POINT'", "backup_id": "'$DB_ID'", "validation": "'$( [ "$RESULT_OK" = 1 ] && echo PASS || echo FAIL )'", "restore_started_at_ns": "'$START_NS'", "restore_ready_at_ns": "'$READY_NS'", "rto_seconds": '$RTO_SECONDS'}'

if [ -n "$RESTORE_REPORT" ]; then
  printf '%s\n' "$RESULT" | jq . > "$RESTORE_REPORT"
fi
printf '%s\n' "$RESULT"

if [ "$RESULT_OK" != 1 ]; then
  die "restore validation FAILED"
fi
info "restore validation PASSED (RTO so far: ${RTO_SECONDS}s)"
echo "RTO_SECONDS=$RTO_SECONDS"