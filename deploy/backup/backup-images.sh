#!/usr/bin/env bash
# backup-images.sh — daily incremental and weekly full backup of the image
# volume.
#
# The shop-mall image volume holds the object-key-addressed PNG/WebP files that
# products and order snapshots reference. A full backup is a GNU tar
# --listed-incremental archive of the volume against a fresh .snar baseline; an
# incremental backup uses tar --listed-incremental against the rolling
# current.snar so each run ships only files changed since the last run AND the
# deletion markers for removed files. Restore replays the last full backup
# followed by every later incremental (each incremental archives its post-state
# .snar so extraction applies additions and deletions exactly).
#
# NOTE: the .snar machinery requires GNU tar. BusyBox tar (the alpine default)
# has no --listed-incremental option, so a GNU-tar image is used — default
# debian:stable-slim, override with TAR_IMAGE.
#
# Output, into <BACKUP_ROOT>/images:
#   <id>.tar.gz            image volume archive
#   <id>.json              metadata (same discipline as backup-db.sh)
#   snar/current.snar      rolling snapshot state (persists between runs)
#   snar/<id>.snar         post-state snapshot archived with each backup
#
# Usage:
#   backup-images.sh [--volume DOCKER_VOLUME] [--backup-root DIR]
#                    [--compose-file FILE] [--project NAME]
#                    [--mode auto|full|incremental] [--full]
set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

require_cmd docker jq

BACKUP_ROOT="${BACKUP_ROOT:-/var/backups/shop-mall}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose/docker-compose.yml}"
PROJECT="${PROJECT:-}"
VOLUME=""
APP_IMAGE="${APP_IMAGE:-unknown}"
MODE="auto"
FULL_WEEKDAY="${FULL_WEEKDAY:-0}"   # 0 = Sunday
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-30}"
TAR_IMAGE="${TAR_IMAGE:-debian:stable-slim}"

usage() {
  cat <<'EOF' >&2
usage: backup-images.sh [options]

  --volume NAME          docker volume to back up (default: resolve 'images'
                         from the compose file)
  --backup-root DIR      backup store path (default: /var/backups/shop-mall)
  --compose-file FILE    compose file resolving the images volume
  --project NAME         compose project name
  --mode auto|full|incremental   auto = full on FULL_WEEKDAY, else incremental
  --full                 force a full backup
  --tar-image IMAGE      GNU-tar-capable image for the .snar machinery
                         (default: debian:stable-slim)
  --help                 show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --volume) VOLUME="$2"; shift 2 ;;
    --backup-root) BACKUP_ROOT="$2"; shift 2 ;;
    --compose-file) COMPOSE_FILE="$2"; shift 2 ;;
    --project) PROJECT="$2"; shift 2 ;;
    --mode) MODE="$2"; shift 2 ;;
    --full) MODE="full"; shift ;;
    --tar-image) TAR_IMAGE="$2"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

# Git Bash: docker needs Windows-style paths for host files.
COMPOSE_FILE="$(host_path "$COMPOSE_FILE")"

if [ -z "$VOLUME" ]; then
  VOLUME="$(resolve_volume "$COMPOSE_FILE" "$PROJECT" images)"
  [ -n "$VOLUME" ] || die "cannot resolve the images volume from $COMPOSE_FILE (pass --volume)"
fi
case "$MODE" in
  auto|full|incremental) ;;
  *) die "--mode must be auto|full|incremental (got '$MODE')" ;;
esac
if [ "$MODE" = "auto" ]; then
  if [ "$(date +%w)" = "$FULL_WEEKDAY" ]; then MODE="full"; else MODE="incremental"; fi
fi

info "volume: $VOLUME  mode: $MODE  backup root: $BACKUP_ROOT"

ID="img-$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_TIME="$(now_iso_utc)"
IMAGES_DIR="$BACKUP_ROOT/images"
STATE_DIR="$BACKUP_ROOT/state"
mkdir -p "$IMAGES_DIR" "$STATE_DIR"

# Optional: record the DB migration version when the postgres service is up.
MIGRATION_VERSION="unknown"
if docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} ps -q postgres >/dev/null 2>&1; then
  PGC="$(service_container "$COMPOSE_FILE" "$PROJECT" postgres 2>/dev/null || true)"
  if [ -n "${PGC:-}" ]; then
    MIGRATION_VERSION="$(migration_version "$PGC" shop_mall 2>/dev/null || echo unknown)"
  fi
fi

mkdir -p "$IMAGES_DIR/snar"
if [ "$MODE" = "full" ]; then
  # A full backup archives the entire tree against a FRESH baseline: removing
  # the rolling current.snar first makes GNU tar list every file now, and the
  # .snar it writes becomes the baseline the next incrementals diff against.
  # The post-state snapshot is archived as snar/<id>.snar for restore replay.
  docker run --rm -v "$VOLUME":/data:ro -v "$(host_path "$IMAGES_DIR")":/backup \
    "$TAR_IMAGE" sh -c \
    "rm -f /backup/snar/current.snar && tar -czf /backup/$ID.tar.gz --listed-incremental=/backup/snar/current.snar -C /data . && cp /backup/snar/current.snar /backup/snar/$ID.snar"
else
  # Each incremental keeps its own snapshot so restore can reconstruct the exact
  # file set: current.snar carries the state forward for the next run and a copy
  # is archived with this backup under snar/<id>.snar. GNU tar writes deletion
  # markers for files removed since the snapshot, so the chain restore is exact.
  docker run --rm -v "$VOLUME":/data:ro -v "$(host_path "$IMAGES_DIR")":/backup \
    "$TAR_IMAGE" sh -c \
    "tar -czf /backup/$ID.tar.gz --listed-incremental=/backup/snar/current.snar -C /data . && cp /backup/snar/current.snar /backup/snar/$ID.snar"
fi

ARTIFACT="$IMAGES_DIR/$ID.tar.gz"
CHECKSUM="$(sha256_file "$ARTIFACT")"

ENC_KEY_VERSION="unencrypted"
if encrypt_enabled; then
  ENC_KEY_VERSION="$(encryption_key_version)"
  encrypt_file "$ARTIFACT" "$ARTIFACT.enc"
  find "$ARTIFACT" -depth -delete
  ARTIFACT="$ARTIFACT.enc"
fi

UPLOAD_LOCATION="${BACKUP_UPLOAD_TARGET:-$BACKUP_ROOT}"
write_backup_marker "$STATE_DIR" images "$(now_epoch)" \
  "backup_id=$ID" \
  "kind=images-${MODE}" \
  "backup_time=$BACKUP_TIME" \
  "app_version=$APP_IMAGE" \
  "migration_version=$MIGRATION_VERSION" \
  "checksum_algorithm=sha256" \
  "checksum=$CHECKSUM" \
  "encryption_key_version=$ENC_KEY_VERSION" \
  "upload_location=$UPLOAD_LOCATION" \
  "volume=$VOLUME" \
  "mode=$MODE" \
  "size_bytes=$(stat -c %s "$ARTIFACT" 2>/dev/null || wc -c < "$ARTIFACT")"

find "$IMAGES_DIR" -maxdepth 1 -type f -name 'img-*.tar.gz*' -mtime "+$BACKUP_RETENTION_DAYS" -delete 2>/dev/null || true

info "images backup complete: $ARTIFACT"
info "  mode=$MODE checksum=$CHECKSUM encryption=$ENC_KEY_VERSION"
echo "IMAGES_BACKUP_ID=$ID"
echo "IMAGES_BACKUP_ARTIFACT=$ARTIFACT"
echo "IMAGES_BACKUP_CHECKSUM=$CHECKSUM"