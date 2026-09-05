/**
 * Playwright webServer launcher for the Go backend.
 *
 * Spawns the prebuilt binary (Makefile `admin-e2e` builds it into
 * e2e/.build/) with the full test environment. The server runs goose
 * migrations from a `migrations` directory relative to its cwd, so the child
 * runs with cwd = backend/. Stays alive until killed; forwards termination to
 * the child so Playwright teardown does not leak processes.
 */

import { spawn } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { BACKEND_PORT, DATABASE_URL } from './lib/env.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '..', '..');
const buildDir = path.join(here, '.build');
const binary = path.join(buildDir, 'server.exe');

function keyring(kid, material) {
  return JSON.stringify({ [kid]: Buffer.from(material, 'ascii').toString('base64') });
}

mkdirSync(path.join(buildDir, 'images'), { recursive: true });

const env = {
  ...process.env,
  APP_ENV: 'development',
  HTTP_ADDR: `127.0.0.1:${BACKEND_PORT}`,
  // Same-origin with the dev server: the /static proxy serves the dev-mode
  // image files back to the browser, so returned image URLs actually load
  // (there is no nginx in the E2E topology).
  PUBLIC_BASE_URL: 'https://localhost:8000',
  // Must match the admin dev server origin: the backend validates the browser
  // Origin header against this allowlist, and the config loader rejects http
  // origins, hence the self-signed https dev server (e2e/certs).
  ADMIN_ALLOWED_ORIGINS: 'https://localhost:8000',
  DATABASE_URL,
  BUYER_JWT_ISSUER: 'buyer-admin-e2e',
  BUYER_JWT_AUDIENCE: 'buyer-admin-e2e',
  BUYER_JWT_ACTIVE_KID: 'buyer-kid',
  BUYER_JWT_KEYRING: keyring('buyer-kid', 'buyer-admin-e2e-key-material-32b'),
  ADMIN_JWT_ISSUER: 'admin-admin-e2e',
  ADMIN_JWT_AUDIENCE: 'admin-admin-e2e',
  ADMIN_JWT_ACTIVE_KID: 'admin-kid',
  ADMIN_JWT_KEYRING: keyring('admin-kid', 'admin-admin-e2e-key-material-32b'),
  UPLOAD_MAX_BYTES: String(2 * 1024 * 1024),
  SIGNUP_BONUS_POINTS: '1000',
  IMAGE_MAX_PIXELS: '25000000',
  IMAGE_STORAGE_CAPACITY_BYTES: String(1 << 30),
  IMAGE_VOLUME_DIR: path.join(buildDir, 'images'),
};

const child = spawn(binary, [], {
  cwd: path.join(repoRoot, 'backend'),
  env,
  stdio: ['ignore', 'inherit', 'inherit'],
});

function killChild() {
  if (child.exitCode !== null) return;
  if (process.platform === 'win32') {
    // spawn without shell still needs a tree kill on Windows.
    spawn('taskkill', ['/pid', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  } else {
    child.kill('SIGTERM');
  }
}
process.on('SIGTERM', killChild);
process.on('SIGINT', killChild);
child.on('exit', (code) => {
  process.exit(code ?? 0);
});
