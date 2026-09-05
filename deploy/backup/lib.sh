#!/usr/bin/env bash
# lib.sh — shared helpers for the shop-mall backup / restore / release scripts.
#
# Sourced by every script under deploy/backup/ and deploy/release/. Provides
# logging, timestamp, checksum, backup-encryption, metadata/marker and
# docker-postgres helpers. Sourcing has no side effects; every function is
# re-entrant.
set -euo pipefail

# Git Bash (MSYS2) silently rewrites POSIX-looking arguments of native binaries
# (docker.exe) into Windows paths, corrupting container-side paths such as
# `psql -f /migrations/...` into `C:/Program Files/Git/migrations/...`. Disable
# that conversion for every docker invocation and convert host paths for -v
# mounts explicitly with host_path().
export MSYS2_ARG_CONV_EXCL='*'
export MSYS_NO_PATHCONV=1

# host_path POSIX_PATH -> Windows path (for docker -v on Git Bash). On native
# Linux/macOS shells this is a pass-through.
host_path() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1"
  else
    printf '%s\n' "$1"
  fi
}

# Resolve the directory the sourced script lives in (deploy/backup), following
# symlinks, so callers can locate the repo root regardless of CWD.
resolve_script_dir() {
  local src="${BASH_SOURCE[0]:-$0}"
  while [ -L "$src" ]; do
    local dir
    dir="$(cd -P "$(dirname "$src")" >/dev/null 2>&1 && pwd)"
    src="$(readlink "$src")"
    case "$src" in /*) ;; *) src="$dir/$src" ;; esac
  done
  cd -P "$(dirname "$src")" >/dev/null 2>&1 && pwd
}

SCRIPT_DIR="$(resolve_script_dir)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." >/dev/null 2>&1 && pwd)"

# --- logging ---------------------------------------------------------------

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }
info() { log "INFO  $*"; }
warn() { log "WARN  $*" >&2; }
die() { log "ERROR $*" >&2; exit 1; }

require_cmd() {
  local bin
  for bin in "$@"; do
    if ! command -v "$bin" >/dev/null 2>&1; then
      die "required command not found: $bin"
    fi
  done
}

# --- time ------------------------------------------------------------------

now_epoch() { date +%s; }
now_ns() { date +%s.%N; }
now_iso_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# --- checksum --------------------------------------------------------------

# sha256_file FILE -> prints the hex digest of FILE.
sha256_file() {
  local f="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$f" | awk '{print $1}'
  else
    openssl dgst -sha256 -r "$f" | awk '{print $1}'
  fi
}

# --- backup encryption -----------------------------------------------------
#
# Encryption is optional and controlled by BACKUP_ENCRYPT:
#   required -> fail hard if BACKUP_ENC_KEY_FILE is not a readable file
#   optional -> encrypt only when the key file is present (default)
#   off      -> never encrypt
# The backup key must be a separate key from the application secrets (Runbook
# section 4). The key file is referenced by path only; its content never
# appears in scripts, metadata or logs.

encrypt_enabled() {
  case "${BACKUP_ENCRYPT:-optional}" in
    required)
      if [ -z "${BACKUP_ENC_KEY_FILE:-}" ] || [ ! -f "$BACKUP_ENC_KEY_FILE" ]; then
        die "BACKUP_ENCRYPT=required but BACKUP_ENC_KEY_FILE is not a readable file"
      fi
      return 0
      ;;
    optional)
      [ -n "${BACKUP_ENC_KEY_FILE:-}" ] && [ -f "$BACKUP_ENC_KEY_FILE" ]
      ;;
    off) return 1 ;;
    *) die "BACKUP_ENCRYPT must be required|optional|off (got '${BACKUP_ENCRYPT:-}')" ;;
  esac
}

# The version recorded in metadata. When the key file is named like
# "backup-key.v3" or "v3" the suffix is extracted; otherwise "custom".
encryption_key_version() {
  if encrypt_enabled; then
    local base
    base="$(basename "${BACKUP_ENC_KEY_FILE:-}")"
    case "$base" in
      *.v[0-9]*)
        echo "$base" | sed -E 's/.*\.(v[0-9]+).*/\1/'
        ;;
      *) echo "custom" ;;
    esac
  else
    echo "unencrypted"
  fi
}

# encrypt_file SRC DST — AES-256-CBC with PBKDF2, key from BACKUP_ENC_KEY_FILE.
encrypt_file() {
  local src="$1" dst="$2"
  require_cmd openssl
  openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt \
    -in "$src" -out "$dst" -pass "file:${BACKUP_ENC_KEY_FILE}"
}

# decrypt_file SRC DST — inverse of encrypt_file.
decrypt_file() {
  local src="$1" dst="$2"
  require_cmd openssl
  openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 \
    -in "$src" -out "$dst" -pass "file:${BACKUP_ENC_KEY_FILE}"
}

# --- metadata --------------------------------------------------------------

# jq_file FILTER PATH — like `jq -r FILTER PATH` but converts the file PATH with
# host_path() before passing it to jq. On Git Bash the jq binary is often a
# native Windows build that cannot open POSIX paths such as /tmp/...; cygpath
# converts them to C:/... for the first positional (file) argument while jq's
# filter string is left untouched. On Linux/macOS host_path() is a pass-through.
jq_file() {
  local filter="$1" path="$2"
  jq -r "$filter" "$(host_path "$path")"
}

# build_metadata KEY=VALUE... -> prints a JSON object with string values.
build_metadata() {
  local obj="{}" kv k v
  for kv in "$@"; do
    k="${kv%%=*}"
    v="${kv#*=}"
    obj="$(jq -nc --argjson o "$obj" --arg k "$k" --arg v "$v" '$o + {($k): $v}')"
  done
  printf '%s\n' "$obj"
}

# write_metadata_json FILE PREFIX KEY=VALUE... — pretty-prints metadata to FILE
# and returns the id (backup_id) via stdout. The PREFIX is echoed verbatim so
# callers can tag which backup kind produced the record.
write_metadata_json() {
  local file="$1" prefix="$2"
  shift 2
  local meta
  meta="$(build_metadata "$@")"
  printf '%s\n' "$meta" | jq . > "$file"
  printf '%s\n' "$prefix"
}

# --- freshness markers -----------------------------------------------------
#
# Every successful backup writes two artefacts into <backup_root>/state:
#   last-<kind>.marker            key=value marker for preflight/freshness
#   backup_last_success.prom      Prometheus textfile for node_exporter
# The .prom file is the documented hook for feeding the backup-freshness gauge
# (see docs/ops/release-recovery.md "备份新鲜度与监控打通"). The in-process
# gauge shop_mall_backup_last_success_timestamp_seconds is fed by the app; the
# recommended production wiring is a host node_exporter textfile collector over
# this directory, because the backup job runs outside the app process.

write_backup_marker() {
  local state_dir="$1" kind="$2" epoch="$3"
  shift 3
  local meta
  meta="$(build_metadata "$@")"
  local id
  id="$(jq -r '.backup_id' <<<"$meta")"
  mkdir -p "$state_dir"
  printf '%s\n' "$meta" | jq . > "$state_dir/${kind}-${id}.json"
  {
    printf 'kind=%s\n' "$kind"
    printf 'backup_id=%s\n' "$id"
    printf 'epoch=%s\n' "$epoch"
    printf 'backup_time=%s\n' "$(jq -r '.backup_time' <<<"$meta")"
    printf 'migration_version=%s\n' "$(jq -r '.migration_version' <<<"$meta")"
    printf 'app_version=%s\n' "$(jq -r '.app_version' <<<"$meta")"
    printf 'checksum=%s\n' "$(jq -r '.checksum' <<<"$meta")"
    printf 'encryption_key_version=%s\n' "$(jq -r '.encryption_key_version' <<<"$meta")"
    printf 'upload_location=%s\n' "$(jq -r '.upload_location' <<<"$meta")"
  } > "$state_dir/last-${kind}.marker"
  if [ "$kind" = "db" ]; then
    cat > "$state_dir/backup_last_success.prom" <<EOF
# HELP shop_mall_backup_last_success_timestamp_seconds Unix timestamp of the last successful database backup.
# TYPE shop_mall_backup_last_success_timestamp_seconds gauge
shop_mall_backup_last_success_timestamp_seconds $epoch
EOF
  fi
}

# marker_age_seconds MARKER_FILE -> prints whole seconds since the marker's
# epoch. Returns 1 (and prints nothing) when the marker is missing or invalid.
marker_age_seconds() {
  local f="$1"
  [ -f "$f" ] || return 1
  local epoch
  epoch="$(awk -F= '/^epoch=/{print $2; exit}' "$f")"
  case "$epoch" in
    ''|*[!0-9]*) return 1 ;;
  esac
  echo "$(( $(date +%s) - epoch ))"
}

# force_wal_advance_epoch CONTAINER DB [WAIT_SECONDS]
#   Forces a WAL segment switch, waits up to WAIT_SECONDS for the archiver to
#   copy the rotated segment, and prints the new pg_stat_archiver
#   last_archived_time as whole seconds since the epoch. Returns 0 on success
#   (position advanced), 1 otherwise. On a dead-quiet server the switch can be
#   a no-op, so a WAL record is emitted to guarantee rotation before retrying.
#   This is the single implementation shared by backup-db.sh (daily) and
#   touch-wal-marker.sh (hourly) so the freshness marker always reflects the
#   ACTUAL archive position, never a cron heartbeat.
force_wal_advance_epoch() {
  local container="$1" db="$2" wait_seconds="${3:-20}"
  local before after i
  before="$(psql_exec "$container" "$db" \
    "SELECT COALESCE(EXTRACT(EPOCH FROM last_archived_time), 0) FROM pg_stat_archiver;")"
  psql_exec "$container" "$db" "SELECT pg_switch_wal();" >/dev/null
  for i in $(seq 1 "$wait_seconds"); do
    after="$(psql_exec "$container" "$db" \
      "SELECT COALESCE(EXTRACT(EPOCH FROM last_archived_time), 0) FROM pg_stat_archiver;")"
    if [ -n "$after" ] && [ "$after" != "0" ] && [ "$after" != "$before" ]; then
      printf '%s' "${after%.*}"
      return 0
    fi
    sleep 1
  done
  psql_exec "$container" "$db" \
    "SELECT pg_logical_emit_message(false, 'shop-mall-backup', 'force-wal-switch');" >/dev/null 2>&1 || true
  psql_exec "$container" "$db" "SELECT pg_switch_wal();" >/dev/null
  for i in $(seq 1 "$wait_seconds"); do
    after="$(psql_exec "$container" "$db" \
      "SELECT COALESCE(EXTRACT(EPOCH FROM last_archived_time), 0) FROM pg_stat_archiver;")"
    if [ -n "$after" ] && [ "$after" != "0" ] && [ "$after" != "$before" ]; then
      printf '%s' "${after%.*}"
      return 0
    fi
    sleep 1
  done
  return 1
}

# write_wal_marker STATE_DIR EPOCH APP_VERSION — refreshes state/last-wal.marker
# with the given archive epoch (whole seconds). The daily backup and the hourly
# touch-wal-marker.sh job both record the archiver's last_archived_time here, so
# preflight.sh's WAL freshness check reflects real archive progress.
write_wal_marker() {
  local state_dir="$1" epoch="${2%.*}" app_version="$3"
  local iso age
  epoch="$(printf '%s' "$epoch" | tr -dc '0-9')"
  [ -n "$epoch" ] || return 1
  iso="$(date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "$epoch")"
  age="$(( $(date +%s) - epoch ))"
  mkdir -p "$state_dir"
  {
    printf 'kind=wal\n'
    printf 'epoch=%s\n' "$epoch"
    printf 'archived_until=%s\n' "$iso"
    printf 'age_seconds=%s\n' "$age"
    printf 'app_version=%s\n' "$app_version"
  } > "$state_dir/last-wal.marker"
}

# --- docker-postgres helpers ----------------------------------------------

# service_container COMPOSE_FILE PROJECT SERVICE -> container id (or id of first
# replica). PROJECT may be empty. Prints an error to stderr and returns 1 when
# the service is not running. NOTE: must use `return`, not die()/exit, so that
# callers can wrap it in `$(... || true)` — an `exit` inside a command
# substitution terminates the subshell before the `||` right side runs.
service_container() {
  local compose_file="$1" project="$2" service="$3"
  local args=(-f "$compose_file")
  [ -n "$project" ] && args+=(-p "$project")
  local id
  id="$(docker compose "${args[@]}" ps -q "$service" 2>/dev/null | head -1 || true)"
  if [ -z "$id" ]; then
    printf 'service "%s" is not running (compose file %s)\n' "$service" "$compose_file" >&2
    return 1
  fi
  printf '%s\n' "$id"
}

# psql_exec CONTAINER DB SQL_ARGS... — runs psql inside a postgres container as
# the postgres OS user over the unix socket (trust auth in the official image),
# so no password is ever needed on the host. The database role defaults to
# POSTGRES_USER (shop_mall); the remaining args are forwarded to psql and the
# standard caller passes the SQL as the next argument.
psql_exec() {
  local container="$1" db="$2" sql="$3"
  shift 3
  docker exec -u postgres "$container" psql -d "$db" -U "${POSTGRES_USER:-shop_mall}" \
    -v ON_ERROR_STOP=1 -At "$@" -c "$sql"
}

# migration_version CONTAINER DB -> prints the highest applied goose version.
migration_version() {
  psql_exec "$1" "$2" \
    "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version;"
}

# compose_app_image COMPOSE_FILE PROJECT -> prints the resolved app image tag.
compose_app_image() {
  local compose_file="$1" project="$2"
  docker compose -f "$compose_file" ${project:+-p "$project"} config \
    --format json 2>/dev/null | jq -r '.services.app.image // empty'
}

# resolve_volume COMPOSE_FILE PROJECT VOLUME_KEY -> prints the actual docker
# volume name (project-prefixed) for a compose volume key.
resolve_volume() {
  local compose_file="$1" project="$2" key="$3"
  docker compose -f "$compose_file" ${project:+-p "$project"} config \
    --format json 2>/dev/null | jq -r --arg k "$key" '.volumes[$k].name // empty'
}

# --- image tag policy ------------------------------------------------------

# is_immutable_tag REF -> 0 when the reference's TAG part matches the release
# whitelist. This is a whitelist, not a blacklist: anything outside the allowed
# shapes fails closed, so a rolling/dev/temp tag such as `:local`, `:latest`,
# `:dev` or a hand-typed tag IS rejected.
# Allowed tag shapes:
#   YYYY.MM.DD[-<build>]      e.g. shop-mall-app:2026.08.13-1, shop-mall-app:2026.08.13-drill
#   <hex sha, 7..64 chars>    e.g. shop-mall-app:3f2a9c0e8d1b4a5c7f6e5d4c3b2a1908f7e6d5c4
# Reference must have exactly one ':' (an explicit tag part).
is_immutable_tag() {
  local ref="${1:-}" tag
  [ -n "$ref" ] || return 1
  tag="${ref##*:}"
  [ -n "$tag" ] && [ "$tag" != "$ref" ] || return 1      # must have an explicit tag
  [ "${ref#*:}" = "$tag" ] || return 1                    # at most one colon (no registry:port)
  case "$tag" in
    *:*|*/*) return 1 ;;
  esac
  if printf '%s' "$tag" | grep -qE '^[0-9]{4}\.[0-9]{2}\.[0-9]{2}(-[A-Za-z0-9._-]+)?$'; then
    return 0
  fi
  if printf '%s' "$tag" | grep -qE '^[0-9a-f]{7,64}$'; then
    return 0
  fi
  return 1
}