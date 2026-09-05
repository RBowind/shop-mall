#!/usr/bin/env bash
# smoke.sh — post-release smoke tests against a deployed system:
#   - /health/live and /health/ready
#   - a buyer wx-login (fake WeChat client when no real AppID is configured)
#   - a public product browse
#   - a points read on the logged-in buyer's profile (/me)
#
# Usage:
#   smoke.sh [--base-url URL] [--login-code CODE]
# Base URL defaults to https://127.0.0.1 (the nginx TLS entrypoint). For the
# restore drill the app is served over plain HTTP on a loopback port.
set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../backup/lib.sh
source "$SCRIPT_DIR/../backup/lib.sh"

require_cmd curl

BASE_URL="${BASE_URL:-https://127.0.0.1}"
LOGIN_CODE="${SMOKE_LOGIN_CODE:-smoke-login-code}"
FAIL=0

usage() {
  cat <<'EOF' >&2
usage: smoke.sh [--base-url URL] [--login-code CODE]
EOF
}
while [ $# -gt 0 ]; do
  case "$1" in
    --base-url) BASE_URL="$2"; shift 2 ;;
    --login-code) LOGIN_CODE="$2"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

PASS() { echo "PASS  $*"; }
FAILX() { echo "FAIL  $*"; FAIL=1; }

echo "---- smoke $(now_iso_utc) against $BASE_URL ----"

STATUS="$(curl -ks -o /dev/null -w '%{http_code}' "$BASE_URL/health/live" 2>/dev/null || true)"
if [ "$STATUS" = "200" ]; then PASS "/health/live 200"; else FAILX "/health/live -> $STATUS (want 200)"; fi

STATUS="$(curl -ks -o /dev/null -w '%{http_code}' "$BASE_URL/health/ready" 2>/dev/null || true)"
if [ "$STATUS" = "200" ]; then PASS "/health/ready 200"; else FAILX "/health/ready -> $STATUS (want 200)"; fi

# buyer wx-login
LOGIN_RESP="$(curl -ks -X POST "$BASE_URL/api/v1/auth/wx-login" \
  -H 'Content-Type: application/json' \
  -d "{\"code\":\"$LOGIN_CODE\"}" 2>/dev/null || true)"
TOKEN="$(printf '%s' "$LOGIN_RESP" | jq -r '.data.access_token // empty' 2>/dev/null || true)"
if [ -n "$TOKEN" ]; then
  PASS "wx-login issued a buyer token"
else
  FAILX "wx-login did not return access_token (response: $(printf '%s' "$LOGIN_RESP" | head -c 200))"
  TOKEN=""
fi

# product browse (public)
STATUS="$(curl -ks -o /dev/null -w '%{http_code}' "$BASE_URL/api/v1/products" 2>/dev/null || true)"
if [ "$STATUS" = "200" ]; then PASS "GET /api/v1/products 200"; else FAILX "GET /api/v1/products -> $STATUS (want 200)"; fi

# points read on the buyer profile
if [ -n "$TOKEN" ]; then
  ME_RESP="$(curl -ks "$BASE_URL/api/v1/me" -H "Authorization: Bearer $TOKEN" 2>/dev/null || true)"
  POINTS="$(printf '%s' "$ME_RESP" | jq -r '.data.points_balance // empty' 2>/dev/null || true)"
  if [ -n "$POINTS" ]; then
    PASS "GET /api/v1/me points_balance=$POINTS"
  else
    FAILX "GET /api/v1/me missing points_balance (response: $(printf '%s' "$ME_RESP" | head -c 200))"
  fi
fi

echo "-----------------------------------"
if [ "$FAIL" = 1 ]; then
  echo "SMOKE RESULT: FAIL"
  exit 1
fi
echo "SMOKE RESULT: PASS"
exit 0