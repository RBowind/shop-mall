#!/usr/bin/env bash
# drill.sh — end-to-end isolated restore drill. Stands up a throwaway source
# PostgreSQL with WAL archiving, seeds it, runs the real backup scripts, writes
# more data (batch A), forces a WAL switch, writes an unrecoverable batch B,
# restores to batch A's WAL position via restore.sh, starts the app against the
# restored instance, runs the release smoke suite, and MEASURES RPO and RTO.
#
# The entire drill lives in the shop-mall-drill docker project; nothing outside
# it is touched and everything is torn down afterwards. RPO target <= 1h, RTO
# target <= 4h. Results are written to <run-dir>/drill-report.json and printed.
#
# Usage:
#   drill.sh [--run-dir DIR] [--keep] [--no-app] [--no-teardown]
set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

require_cmd docker jq date curl

BACKUP_SCRIPT="$SCRIPT_DIR/backup-db.sh"
IMAGES_BACKUP_SCRIPT="$SCRIPT_DIR/backup-images.sh"
RESTORE_SCRIPT="$SCRIPT_DIR/restore.sh"
SMOKE_SCRIPT="$REPO_ROOT/deploy/release/smoke.sh"
COMPOSE_FILE="$REPO_ROOT/deploy/compose/docker-compose.drill.yml"

PROJECT="shop-mall-drill"
NETWORK="${PROJECT}_drill"
SOURCE_CONTAINER="${PROJECT}-source-db"
RESTORE_CONTAINER="${PROJECT}-restore-db"
APP_CONTAINER="${PROJECT}-app"
WAL_VOLUME="${PROJECT}_wal"
BACKUP_VOLUME="${PROJECT}_backup"
# Source/mutated image volume (seeded + mutated by the incremental-chain phase).
IMAGES_VOLUME="${PROJECT}_images"
# Isolated restore target volume: restore.sh replays the full+incremental chain
# into a FRESH volume, and the app mounts this restored copy.
RESTORE_IMAGES_VOLUME="${PROJECT}_images-restored"
POSTGRES_IMAGE="postgres:16-alpine"
# Whitelisted-shaped release tag (is_immutable_tag requires YYYY.MM.DD[-\<build>]
# or a hex sha). The drill images the app under this tag so preflight/rollback
# semantics stay consistent; a plain `:local` is deliberately rejected now.
APP_IMAGE="${APP_IMAGE:-shop-mall-app:2026.08.13-drill}"
APP_PORT="${APP_PORT:-18080}"
DB_NAME="shop_mall"
POSTGRES_USER="${POSTGRES_USER:-shop_mall}"
# Image-volume chain backup/restore runs on a GNU-tar-capable image (BusyBox tar
# has no --listed-incremental); TAR_IMAGE must stay consistent with what
# backup-images.sh and restore.sh use.
TAR_IMAGE="${TAR_IMAGE:-debian:stable-slim}"

KEEP=0
START_APP=1
TEARDOWN=1
RUN_DIR="${DRILL_RUN_DIR:-}"

usage() {
  cat <<'EOF' >&2
usage: drill.sh [options]

  --run-dir DIR         keep the run's backup + report under DIR (default: mktemp)
  --keep                do not tear down the drill project afterwards
  --no-app              run the DB+images restore only (skip the app + smoke)
  --no-teardown         leave the drill project running (implies --keep)
  --help                show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --run-dir) RUN_DIR="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --no-app) START_APP=0; shift ;;
    --no-teardown) KEEP=1; TEARDOWN=0; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

if [ -z "$RUN_DIR" ]; then
  RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/shop-mall-drill.XXXXXX")"
fi
# Git Bash: docker needs Windows-style paths for host files.
COMPOSE_FILE="$(host_path "$COMPOSE_FILE")"
HOST_BACKUP_ROOT="$RUN_DIR/backup"
# The backup store is bind-mounted into the source container at /backup so the
# real backup scripts can write artifacts the host checksums and restores.
export DRILL_BACKUP_ROOT="$(host_path "$HOST_BACKUP_ROOT")"
TRANSCRIPT="$RUN_DIR/drill-transcript.log"
REPORT_FILE="$RUN_DIR/drill-report.json"
RESTORE_REPORT="$RUN_DIR/restore-report.json"
mkdir -p "$HOST_BACKUP_ROOT"

# --- logging helper that mirrors to the transcript -------------------------

log() { local line; line="$(printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*")"; printf '%s\n' "$line"; printf '%s\n' "$line" >> "$TRANSCRIPT"; }

# --- teardown --------------------------------------------------------------

drill_teardown() {
  docker rm -f "$APP_CONTAINER" "$RESTORE_CONTAINER" >/dev/null 2>&1 || true
  docker compose -f "$COMPOSE_FILE" -p "$PROJECT" down -v --remove-orphans >/dev/null 2>&1 || true
  # restore.sh (postgres-data, images-restored) and phase_images_seed (images)
  # create these via `docker volume create`, so compose `down -v` does not track
  # them; remove them explicitly.
  docker volume rm -f "${PROJECT}_postgres-data" "$RESTORE_IMAGES_VOLUME" "$IMAGES_VOLUME" >/dev/null 2>&1 || true
}

# --- phase 1: source up + migrate + seed -----------------------------------

phase_source() {
  log "phase 1/9: standing up isolated source postgres (WAL archiving on)"
  docker compose -f "$COMPOSE_FILE" -p "$PROJECT" up -d source-db
  local n=0
  until docker exec -u postgres "$SOURCE_CONTAINER" pg_isready -d "$DB_NAME" >/dev/null 2>&1; do
    n=$((n + 1)); [ "$n" -gt 60 ] && die "source postgres did not become ready"; sleep 2
  done
  log "  source postgres ready; running forward migrations"
  # The WAL archive volume is fresh and root-owned; the archiver runs as the
  # postgres OS user, so make /wal writable or every archive_command fails.
  docker exec -u root "$SOURCE_CONTAINER" sh -c 'chown postgres:postgres /wal'
  docker exec -u postgres "$SOURCE_CONTAINER" psql -d "$DB_NAME" -U "$POSTGRES_USER" -v ON_ERROR_STOP=1 -f /migrations/0001_init.up.sql
  docker exec -u postgres "$SOURCE_CONTAINER" psql -d "$DB_NAME" -U "$POSTGRES_USER" -v ON_ERROR_STOP=1 -f /migrations/0002_seed.up.sql
  # Production applies migrations through goose, which records applied versions
  # in goose_db_version. The drill applies the same SQL directly, so record the
  # version table explicitly to mirror the production state (preflight and the
  # restore validation read it).
  docker exec -i -u postgres "$SOURCE_CONTAINER" psql -d "$DB_NAME" -U "$POSTGRES_USER" -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE goose_db_version (
    id         SERIAL PRIMARY KEY,
    version_id BIGINT NOT NULL,
    is_applied BOOLEAN NOT NULL,
    tstamp     TIMESTAMP DEFAULT now()
);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (1, true), (2, true);
SQL
  log "  seeding deterministic drill data"
  docker exec -i -u postgres "$SOURCE_CONTAINER" psql -d "$DB_NAME" -U "$POSTGRES_USER" -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO products (name, description, main_image, price_points, stock, status) VALUES
  ('Drill Product A', 'drill seed', '11111111-1111-1111-1111-111111111111.png', 20, 100, 'on_sale'),
  ('Drill Product B', 'drill seed', '22222222-2222-2222-2222-222222222222.png', 100, 50, 'on_sale'),
  ('Drill Product C', 'drill seed', '33333333-3333-3333-3333-333333333333.png', 5, 200, 'off_sale'),
  ('Drill Product Missing', 'drill seed', '99999999-9999-9999-9999-999999999999.png', 7, 30, 'on_sale');

INSERT INTO users (openid, nickname, points_balance) VALUES
  ('drill-openid-a', 'Drill A', 100),
  ('drill-openid-b', 'Drill B', 60),
  ('drill-openid-c', 'Drill C', 0);

INSERT INTO orders (id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, paid_at) VALUES
  (1, 'DRILL-SEED-0001', 2, 'drill-seed-tok-1', repeat('a', 64), 'paid', 40, 'Receiver B', '13800000002', 'Drill Address B', now()),
  (2, 'DRILL-SEED-0002', 3, 'drill-seed-tok-2', repeat('b', 64), 'paid', 100, 'Receiver C', '13800000003', 'Drill Address C', now());

INSERT INTO order_items (order_id, product_id, product_name, product_image, price_snapshot, quantity) VALUES
  (1, 1, 'Drill Product A', '11111111-1111-1111-1111-111111111111.png', 20, 2),
  (2, 2, 'Drill Product B', '22222222-2222-2222-2222-222222222222.png', 100, 1);

INSERT INTO points_ledger (user_id, order_id, event_key, type, delta, balance_after) VALUES
  (1, NULL, 'signup-a', 'signup_bonus', 100, 100),
  (2, NULL, 'signup-b', 'signup_bonus', 100, 100),
  (3, NULL, 'signup-c', 'signup_bonus', 100, 100),
  (2, 1, 'pay-seed-1', 'order_pay', -40, 60),
  (3, 2, 'pay-seed-2', 'order_pay', -100, 0);

-- The seed inserted explicit order ids; advance the sequence so later inserts
-- (batch A/B) do not collide with id 1/2.
SELECT setval(pg_get_serial_sequence('orders', 'id'), (SELECT MAX(id) FROM orders));
SQL
  log "  seeded: 4 products, 3 users, 2 orders, 5 ledger rows"
}

# --- phase 2: seed the image volume ----------------------------------------

phase_images_seed() {
  log "phase 2/11: seeding the image volume ($IMAGES_VOLUME)"
  docker volume create "$IMAGES_VOLUME" >/dev/null
  docker run --rm -v "$IMAGES_VOLUME":/data alpine sh -c '
    for f in 11111111-1111-1111-1111-111111111111.png 22222222-2222-2222-2222-222222222222.png 33333333-3333-3333-3333-333333333333.png 44444444-4444-4444-4444-444444444444.png; do
      printf "drill-image %s\n" "$f" > /data/"$f"
    done
  '
  log "  seeded 4 image files (9999...png intentionally absent -> referenced-but-missing alert)"
}

# --- phase 3: backups -------------------------------------------------------

phase_backup() {
  log "phase 3/11: running the real backup scripts (images full + db)"
  bash "$IMAGES_BACKUP_SCRIPT" --volume "$IMAGES_VOLUME" --backup-root "$HOST_BACKUP_ROOT" --full 2>&1 | tee -a "$TRANSCRIPT"
  BACKUP_DB_OUT="$(BACKUP_UPLOAD_TARGET="$HOST_BACKUP_ROOT" \
    bash "$BACKUP_SCRIPT" --container "$SOURCE_CONTAINER" --backup-root "$HOST_BACKUP_ROOT" 2>&1 | tee -a "$TRANSCRIPT")"
  log "  host store: $HOST_BACKUP_ROOT"
}

# --- phase 4: incremental image chain ---------------------------------------
# Exercises the previously-unexecuted incremental path: mutate the source volume
# (add a file, delete the orphan), take an incremental backup, mutate again
# (add a different file, delete the previous addition so a deletion marker must
# be recorded), take a second incremental. DB-referenced keys 1111/2222/3333
# are intentionally left intact; 9999 stays absent (referenced-but-missing).

phase_images_chain() {
  log "phase 4/11: incremental image backup chain (2 mutations, 2 incrementals)"
  docker run --rm -v "$IMAGES_VOLUME":/data alpine sh -c '
    rm -f /data/44444444-4444-4444-4444-444444444444.png
    printf "drill-image 5555" > /data/55555555-5555-5555-5555-555555555555.png
  '
  log "  mutation 1: deleted 4444 (orphan), added 5555"
  bash "$IMAGES_BACKUP_SCRIPT" --volume "$IMAGES_VOLUME" --backup-root "$HOST_BACKUP_ROOT" --mode incremental 2>&1 | tee -a "$TRANSCRIPT"
  docker run --rm -v "$IMAGES_VOLUME":/data alpine sh -c '
    rm -f /data/55555555-5555-5555-5555-555555555555.png
    printf "drill-image 6666" > /data/66666666-6666-6666-6666-666666666666.png
  '
  log "  mutation 2: deleted 5555, added 6666"
  bash "$IMAGES_BACKUP_SCRIPT" --volume "$IMAGES_VOLUME" --backup-root "$HOST_BACKUP_ROOT" --mode incremental 2>&1 | tee -a "$TRANSCRIPT"
  log "  chain created: full + 2 incrementals; source volume now has 1111,2222,3333,6666"
}

# --- phase 5: batch A + WAL switch ------------------------------------------

phase_batch_a() {
  log "phase 5/11: batch A (recoverable) writes after backup"
  docker exec -i -u postgres "$SOURCE_CONTAINER" psql -d "$DB_NAME" -U "$POSTGRES_USER" -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO orders (order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, paid_at)
SELECT 'DRILL-A-' || lpad(i::text, 6, '0'), 1, 'drill-batch-a-tok-' || i, repeat(md5(i::text), 2), 'paid', 1, 'Batch A Receiver', '13800000001', 'Batch A Address', now()
FROM generate_series(1, 50) AS i;

INSERT INTO order_items (order_id, product_id, product_name, product_image, price_snapshot, quantity)
SELECT o.id, 4, 'Drill Product Missing', '99999999-9999-9999-9999-999999999999.png', 1, 1
FROM orders o WHERE o.order_no LIKE 'DRILL-A-%';

INSERT INTO points_ledger (user_id, order_id, event_key, type, delta, balance_after)
SELECT 1, o.id, 'pay-a-' || o.id, 'order_pay', -1, 100 - rn
FROM orders o
JOIN (SELECT id, row_number() OVER (ORDER BY id) AS rn FROM orders WHERE order_no LIKE 'DRILL-A-%') r ON r.id = o.id;

UPDATE users SET points_balance = 50 WHERE id = 1;
SQL
  T1="$(now_ns)"
  log "  batch A committed: 50 orders (user1 balance -> 50)"
}

phase_wal_switch() {
  log "phase 6/11: forcing a WAL switch and confirming the archiver"
  local before after n
  # The restore point is the LSN of the last write BEFORE the switch: it sits at
  # the tail of segment N, which the archiver rotates and copies as soon as the
  # switch completes. pg_switch_wal() itself returns the START LSN of the NEW
  # segment (N+1), which is only archived when N+1 later rotates — a recovery
  # target beyond the durable archive makes postgres FATAL ("recovery ended
  # before configured recovery target was reached"). Taking pg_current_wal_lsn()
  # BEFORE the switch guarantees the target lies inside an archived segment.
  RESTORE_LSN="$(psql_exec "$SOURCE_CONTAINER" "$DB_NAME" "SELECT pg_current_wal_lsn();")"
  # Snapshot the archiver BEFORE the switch so the switch's own archived segment
  # is the change we detect.
  before="$(psql_exec "$SOURCE_CONTAINER" "$DB_NAME" \
    "SELECT COALESCE(EXTRACT(EPOCH FROM last_archived_time), 0) FROM pg_stat_archiver;")"
  SWITCH_LSN="$(psql_exec "$SOURCE_CONTAINER" "$DB_NAME" "SELECT pg_switch_wal();")"
  log "  pre-switch LSN (restore target): $RESTORE_LSN"
  log "  switch LSN: $SWITCH_LSN"
  for n in $(seq 1 30); do
    after="$(psql_exec "$SOURCE_CONTAINER" "$DB_NAME" \
      "SELECT COALESCE(EXTRACT(EPOCH FROM last_archived_time), 0) FROM pg_stat_archiver;")"
    if [ -n "$after" ] && [ "$after" != "0" ] && [ "$after" != "$before" ]; then
      T2="$(now_ns)"
      log "  archiver confirmed WAL segment (RPO window starts after this point)"
      return 0
    fi
    sleep 1
  done
  die "archiver did not confirm the WAL switch; archive_command may be broken"
}

# --- phase 6: batch B (unrecoverable window) --------------------------------

phase_batch_b() {
  log "phase 7/11: batch B writes (simulated post-archive outage window)"
  docker exec -i -u postgres "$SOURCE_CONTAINER" psql -d "$DB_NAME" -U "$POSTGRES_USER" -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO orders (order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, paid_at)
SELECT 'DRILL-B-' || lpad(i::text, 6, '0'), 1, 'drill-batch-b-tok-' || i, repeat(md5(concat('b', i::text)), 2), 'paid', 1, 'Batch B Receiver', '13800000001', 'Batch B Address', now()
FROM generate_series(1, 10) AS i;

INSERT INTO order_items (order_id, product_id, product_name, product_image, price_snapshot, quantity)
SELECT o.id, 4, 'Drill Product Missing', '99999999-9999-9999-9999-999999999999.png', 1, 1
FROM orders o WHERE o.order_no LIKE 'DRILL-B-%';

INSERT INTO points_ledger (user_id, order_id, event_key, type, delta, balance_after)
SELECT 1, o.id, 'pay-b-' || o.id, 'order_pay', -1, 50 - rn
FROM orders o
JOIN (SELECT id, row_number() OVER (ORDER BY id) AS rn FROM orders WHERE order_no LIKE 'DRILL-B-%') r ON r.id = o.id;

UPDATE users SET points_balance = 40 WHERE id = 1;
SQL
  T3="$(now_ns)"
  ORDERS_LIVE="$(psql_exec "$SOURCE_CONTAINER" "$DB_NAME" "SELECT count(*) FROM orders;")"
  ORDERS_EXPECTED="$((ORDERS_LIVE - 10))"
  log "  batch B committed: 10 orders (user1 balance -> 40). live orders=$ORDERS_LIVE"
  log "  RPO window: last archive -> batch B = $([ -n "$T2" ] && awk -v a="$T3" -v b="$T2" 'BEGIN{printf "%.3f", a-b}')s"
}

# --- phase 7: restore via restore.sh -----------------------------------------

phase_restore() {
  log "phase 8/11: isolated restore to LSN $RESTORE_LSN (restore.sh)"
  RESTORE_OUT="$(RESTORE_REPORT="$RESTORE_REPORT" \
    bash "$RESTORE_SCRIPT" \
      --backup-root "$HOST_BACKUP_ROOT" \
      --project "$PROJECT" \
      --restore-point "$RESTORE_LSN" \
      --wal-volume "$WAL_VOLUME" \
      --images-volume "$RESTORE_IMAGES_VOLUME" \
      --network "$NETWORK" \
      --restore-container "$RESTORE_CONTAINER" \
      --expect-orders "$ORDERS_EXPECTED" \
      --expect-users 3 \
      --expect-products 4 2>&1 | tee -a "$TRANSCRIPT")"
  RESTORE_READY_NS="$(printf '%s\n' "$RESTORE_OUT" | grep '^RTO_SECONDS=' | tail -1)"
  RESTORE_READY_NS="${RESTORE_READY_NS#RTO_SECONDS=}"
  log "  restore.sh finished (RTO to validation: ${RESTORE_READY_NS}s)"
}

# --- phase 9: assert the restored image volume -------
# Verifies the full+incremental chain replay produced EXACTLY the expected file
# set: DB-referenced 1111/2222/3333 present, 9999 absent (referenced-but-missing
# alert in restore.sh), and the chain's added-then-deleted files (4444, 5555)
# gone while the last addition (6666) is the only orphan. This closes the brief
# item 2 gap: the incremental machinery is executed and its deletion semantics
# are asserted, not just exercised by the *-full path.

phase_images_assert() {
  log "phase 9/11: asserting the restored image volume matches the expected set"
  local restored
  restored="$(docker run --rm -v "$RESTORE_IMAGES_VOLUME":/data:ro "$TAR_IMAGE" \
    find /data -type f 2>/dev/null | sed 's#^/data/##' | LC_ALL=C sort)"
  log "  restored image files:"
  printf '%s\n' "$restored" | sed 's/^/      /'
  local expected want ok=1
  want="11111111-1111-1111-1111-111111111111.png 22222222-2222-2222-2222-222222222222.png 33333333-3333-3333-3333-333333333333.png 66666666-6666-6666-6666-666666666666.png"
  for expected in $want; do
    if printf '%s\n' "$restored" | grep -qx "$expected"; then
      log "  PASS  restored image present: $expected"
    else
      log "  FAIL  restored image missing: $expected"
      ok=0
    fi
  done
  for stale in 44444444-4444-4444-4444-444444444444.png 55555555-5555-5555-5555-555555555555.png 99999999-9999-9999-9999-999999999999.png; do
    if printf '%s\n' "$restored" | grep -qx "$stale"; then
      log "  FAIL  restored image should NOT exist: $stale"
      ok=0
    fi
  done
  if [ "$ok" = 1 ]; then
    log "IMG_CHAIN=PASS restored volume matches full+incremental expected set"
  else
    log "IMG_CHAIN=FAIL"
    die "restored image volume does not match the expected incremental-chain set"
  fi
}

# --- phase 10: app + smoke ---------------------------------------------------

phase_app() {
  log "phase 10/11: starting the app against the restored instance"
  local buyer_key admin_key
  buyer_key="$(printf '%s' 'drill-buyer-key-material-000000000000000000000000000000000000' | base64 -w0)"
  admin_key="$(printf '%s' 'drill-admin-key-material-000000000000000000000000000000000000' | base64 -w0)"
  local buyer_keyring admin_keyring
  buyer_keyring="$(jq -nc --arg k "$buyer_key" '{("drill-kid-buyer"): $k}')"
  admin_keyring="$(jq -nc --arg k "$admin_key" '{("drill-kid-admin"): $k}')"

  docker rm -f "$APP_CONTAINER" >/dev/null 2>&1 || true
  docker run -d --name "$APP_CONTAINER" \
    --network "$NETWORK" \
    -p "127.0.0.1:$APP_PORT:8080" \
    -e APP_ENV=development \
    -e HTTP_ADDR=:8080 \
    -e PUBLIC_BASE_URL=https://api.drill.test \
    -e ADMIN_ALLOWED_ORIGINS=https://admin.drill.test \
    -e DATABASE_URL="postgres://shop_mall:drill@${RESTORE_CONTAINER}:5432/${DB_NAME}?sslmode=disable" \
    -e BUYER_JWT_ISSUER=drill-buyer-issuer \
    -e BUYER_JWT_AUDIENCE=drill-buyer-audience \
    -e BUYER_JWT_ACTIVE_KID=drill-kid-buyer \
    -e "BUYER_JWT_KEYRING=$buyer_keyring" \
    -e BUYER_JWT_TTL=24h \
    -e ADMIN_JWT_ISSUER=drill-admin-issuer \
    -e ADMIN_JWT_AUDIENCE=drill-admin-audience \
    -e ADMIN_JWT_ACTIVE_KID=drill-kid-admin \
    -e "ADMIN_JWT_KEYRING=$admin_keyring" \
    -e ADMIN_JWT_TTL=30m \
    -e UPLOAD_MAX_BYTES=2097152 \
    -e SIGNUP_BONUS_POINTS=100 \
    -e IMAGE_MAX_PIXELS=25000000 \
    -e IMAGE_STORAGE_CAPACITY_BYTES=1073741824 \
    -e IMAGE_GRACE_PERIOD=720h \
    -e IMAGE_CLEANUP_INTERVAL=24h \
    -e IMAGE_VOLUME_DIR=/var/lib/shop-mall/images \
    -v "$RESTORE_IMAGES_VOLUME":/var/lib/shop-mall/images \
    "$APP_IMAGE" >/dev/null

  local n=0
  until curl -sf "http://127.0.0.1:$APP_PORT/health/ready" >/dev/null 2>&1; do
    n=$((n + 1)); [ "$n" -gt 90 ] && die "app did not become ready on port $APP_PORT"; sleep 2
    docker logs "$APP_CONTAINER" 2>&1 | tail -3 >> "$TRANSCRIPT" || true
  done
  APP_READY_NS="$(now_ns)"
  log "  app /health/ready OK (port $APP_PORT)"
  log "  running release smoke suite"
  bash "$SMOKE_SCRIPT" --base-url "http://127.0.0.1:$APP_PORT" 2>&1 | tee -a "$TRANSCRIPT"
}

# --- phase 9: observation + report -------------------------------------------

phase_observe() {
  log "phase 11/11: post-restore observation window"
  OBSERVE_START="$(now_ns)"
  # points reconciliation on the restored instance (must stay clean)
  local m
  m="$(psql_exec "$RESTORE_CONTAINER" "$DB_NAME" \
    "SELECT count(*) FROM (SELECT u.id FROM users u LEFT JOIN points_ledger l ON l.user_id = u.id GROUP BY u.id, u.points_balance HAVING u.points_balance <> COALESCE(SUM(l.delta), 0)) x;")"
  if [ "$m" = "0" ]; then log "  observation: points reconciliation still clean"; else log "  observation: points reconciliation DRIFT ($m)"; fi
  if curl -sf "http://127.0.0.1:$APP_PORT/health/ready" >/dev/null 2>&1; then
    log "  observation: app /health/ready still OK"
  else
    log "  observation: app /health/ready FAILED"
  fi
  APP_READY_NS="${APP_READY_NS:-$OBSERVE_START}"
  OBSERVE_END="$(now_ns)"
}

write_report() {
  local rpo rto_total rto_valid obs
  rpo="$(awk -v a="$T3" -v b="$T2" 'BEGIN{printf "%.3f", a-b}')"
  rto_total="$(awk -v a="$APP_READY_NS" -v b="${RESTORE_START_NS:-$START_NS}" 'BEGIN{printf "%.3f", a-b}')"
  rto_valid="${RESTORE_READY_NS:-0}"
  obs="$(awk -v a="$OBSERVE_END" -v b="$OBSERVE_START" 'BEGIN{printf "%.3f", a-b}')"
  local rpo_met rto_met
  rpo_met="$(awk -v v="$rpo" 'BEGIN{print (v <= 3600) ? "true" : "false"}')"
  rto_met="$(awk -v v="$rto_total" 'BEGIN{print (v <= 14400) ? "true" : "false"}')"
  jq -n \
    --arg date "$(now_iso_utc)" \
    --argjson rpo "$rpo" --argjson rpo_target 3600 --arg rpo_met "$rpo_met" \
    --argjson rto "$rto_total" --argjson rto_target 14400 --arg rto_met "$rto_met" \
    --argjson rto_validation "$rto_valid" \
    --arg restore_point "${RESTORE_LSN:-$SWITCH_LSN}" \
    --argjson orders_live "$ORDERS_LIVE" --argjson orders_restored "$ORDERS_EXPECTED" \
    --argjson orders_lost "$((ORDERS_LIVE - ORDERS_EXPECTED))" \
    --argjson observation_seconds "$obs" \
    --arg run_dir "$RUN_DIR" \
    '{drill_date: $date, restore_point_lsn: $restore_point,
      orders_live: $orders_live, orders_restored: $orders_restored, orders_lost: $orders_lost,
      rpo_seconds: $rpo, rpo_target_seconds: $rpo_target, rpo_met: ($rpo_met == "true"),
      rto_seconds: $rto, rto_validation_seconds: $rto_validation, rto_target_seconds: $rto_target, rto_met: ($rto_met == "true"),
      observation_seconds: $observation_seconds, run_dir: $run_dir}' > "$REPORT_FILE"
  log "  drill report: $REPORT_FILE"
  log "  RPO = ${rpo}s (target <= 3600s) met=$rpo_met"
  log "  RTO = ${rto_total}s (target <= 14400s) met=$rto_met"
}

# --- main --------------------------------------------------------------------

START_NS="$(now_ns)"
log "==== shop-mall restore drill ===="
log "run dir: $RUN_DIR"

# Clean any stale drill resources from a previous interrupted run.
drill_teardown

# The drill runs the app under a whitelisted-shaped release tag; make sure that
# exact tag exists (built images live locally as shop-mall-app:local).
if ! docker image inspect "$APP_IMAGE" >/dev/null 2>&1; then
  docker tag shop-mall-app:local "$APP_IMAGE" 2>/dev/null \
    || die "cannot tag shop-mall-app:local as $APP_IMAGE (build deploy/compose/Dockerfile first)"
fi
# GNU tar image for the incremental imagechain; ensure it is available.
docker image inspect "$TAR_IMAGE" >/dev/null 2>&1 \
  || docker pull "$TAR_IMAGE" >/dev/null 2>&1 \
  || die "GNU tar image $TAR_IMAGE not available"

phase_source
phase_images_seed
phase_backup
phase_images_chain
phase_batch_a
phase_wal_switch
phase_batch_b
RESTORE_START_NS="$(now_ns)"
phase_restore
phase_images_assert
if [ "$START_APP" = 1 ]; then
  phase_app
else
  APP_READY_NS="$(now_ns)"
fi
phase_observe
write_report

if [ "$KEEP" = 1 ]; then
  log "drill resources kept (--keep); restore container: $RESTORE_CONTAINER"
else
  log "tearing down the drill project"
  drill_teardown
  log "drill project removed"
fi

log "==== drill complete ===="
cat "$REPORT_FILE"