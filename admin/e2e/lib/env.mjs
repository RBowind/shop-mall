/**
 * Shared E2E environment resolution (Node side).
 *
 * Ports are fixed by default so the whole loop is deterministic; every knob can
 * be overridden through ADMIN_E2E_* environment variables when a port is
 * already taken on the machine.
 */

function intEnv(name, fallback) {
  const raw = process.env[name];
  if (!raw) return fallback;
  const value = Number.parseInt(raw, 10);
  return Number.isInteger(value) ? value : fallback;
}

export const PG_PORT = intEnv('ADMIN_E2E_PG_PORT', 15432);
export const BACKEND_PORT = intEnv('ADMIN_E2E_BACKEND_PORT', 18080);
export const DEV_READY_PORT = intEnv('ADMIN_E2E_DEV_READY_PORT', 18081);
export const APP_PORT = intEnv('ADMIN_E2E_APP_PORT', 8000);

/** Admin dev server (what the browser talks to; /api is proxied to backend). */
export const APP_URL = `https://localhost:${APP_PORT}`;
/** Backend directly, bypassing the dev proxy. Node-side seeding uses this. */
export const BACKEND_URL = `http://127.0.0.1:${BACKEND_PORT}`;

export const DATABASE_URL = `host=127.0.0.1 port=${PG_PORT} user=test password=test dbname=shop_mall_test sslmode=disable`;

export const PG_CONTAINER = 'shop-mall-admin-e2e-pg';

export const SUPER_USERNAME = 'super-e2e';
export const SUPER_PASSWORD = 'Admin#E2e2026pwd!';
export const VIEWER_USERNAME = 'viewer-e2e';
export const VIEWER_PASSWORD = 'Viewer#E2e2026pwd!';
