#!/usr/bin/env bash
# rollback.sh — rehearse switching the app back to a previous IMMUTABLE image
# tag. Never runs `goose down`: migrations are forward-only, so a code-level
# rollback only swaps the image tag, restarts the app and re-checks health + the
# smoke suite. Database-structure repairs are handled by a new forward migration
# (Runbook section 6).
#
# Usage:
#   rollback.sh <previous-image-tag> [--compose-file FILE] [--project NAME] [--no-smoke]
#
# Example:
#   rollback.sh shop-mall-app:2026.08.12-1
set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../backup/lib.sh
source "$SCRIPT_DIR/../backup/lib.sh"

require_cmd docker jq

COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose/docker-compose.yml}"
PROJECT="${PROJECT:-}"
RUN_SMOKE=1

PREVIOUS_TAG="${1:-}"
if [ -z "$PREVIOUS_TAG" ]; then
  echo "usage: rollback.sh <previous-image-tag> [--no-smoke]" >&2
  exit 2
fi
shift

while [ $# -gt 0 ]; do
  case "$1" in
    --compose-file) COMPOSE_FILE="$2"; shift 2 ;;
    --project) PROJECT="$2"; shift 2 ;;
    --no-smoke) RUN_SMOKE=0; shift ;;
    *) die "unknown argument: $1" ;;
  esac
done

# Git Bash: docker needs Windows-style paths for host files.
COMPOSE_FILE="$(host_path "$COMPOSE_FILE")"

if ! is_immutable_tag "$PREVIOUS_TAG"; then
  die "refusing to roll back to a rolling/mutable tag: $PREVIOUS_TAG"
fi

STATE_DIR="$REPO_ROOT/deploy/release/state"
mkdir -p "$STATE_DIR"
CURRENT_IMAGE="$(compose_app_image "$COMPOSE_FILE" "$PROJECT")"
NOW="$(now_iso_utc)"

echo "---- rollback rehearsal ----"
echo "current image: ${CURRENT_IMAGE:-unknown}"
echo "rollback image: $PREVIOUS_TAG (no goose down; migrations are forward-only)"

# Swap the tag without touching the database.
APP_IMAGE="$PREVIOUS_TAG" docker compose -f "$COMPOSE_FILE" ${PROJECT:+-p "$PROJECT"} up -d app
echo "app recreated with $PREVIOUS_TAG"

# Wait for health.
n=0
until curl -ks -o /dev/null -w '' --max-time 3 "https://127.0.0.1/health/ready" 2>/dev/null; do
  n=$((n + 1)); [ "$n" -gt 90 ] && { echo "FAIL  app did not become ready after rollback" >&2; exit 1; }
  sleep 2
done
echo "PASS  /health/ready after rollback"

if [ "$RUN_SMOKE" = 1 ]; then
  bash "$SCRIPT_DIR/smoke.sh" 2>&1 || exit 1
fi

# Record what happened (immutable audit of the rehearsal).
{
  printf 'rollback_time=%s\n' "$NOW"
  printf 'from_image=%s\n' "${CURRENT_IMAGE:-unknown}"
  printf 'to_image=%s\n' "$PREVIOUS_TAG"
  printf 'goose_down_used=no\n'
} > "$STATE_DIR/last-rollback.marker"
echo "rollback recorded in $STATE_DIR/last-rollback.marker"
echo "ROLLBACK RESULT: PASS"