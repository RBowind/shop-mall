#!/usr/bin/env bash
# Regenerate the local self-signed HTTPS certificate used by the admin E2E
# dev server (see admin/.umirc.ts https block). The private key is never
# committed: a fresh clone has an empty certs/ dir, and this script (wired
# into `make admin-e2e`) creates the pair on demand. Re-running is a no-op
# once the files exist, so the cert stays stable across e2e runs.
set -euo pipefail
cd "$(dirname "$0")"
if [ -f dev-cert.pem ] && [ -f dev-key.pem ]; then
  echo "admin e2e certs already present, skipping generation"
  exit 0
fi
# MSYS_NO_PATHCONV=1 keeps /CN=localhost intact under Git Bash on Windows.
MSYS_NO_PATHCONV=1 openssl req -x509 -newkey rsa:2048 -sha256 -nodes \
  -keyout dev-key.pem -out dev-cert.pem \
  -days 3650 -subj '/CN=localhost'
echo "generated dev-cert.pem / dev-key.pem (CN=localhost, 10 years)"
