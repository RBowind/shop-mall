/**
 * Playwright config for the admin E2E loop.
 *
 * One worker, sequential: all specs share the deterministic seed state written
 * by globalSetup (one superadmin, one viewer, one paid order), and slice-1
 * cases mutate that shared state (P6 toggles product A, O3 ships the seed
 * order). Parallel workers would race those mutations.
 *
 * The two webServers are started (and kept alive across runs via
 * reuseExistingServer) in this order:
 *   1. Go backend on 127.0.0.1:18080 (prebuilt by `make admin-e2e`)
 *   2. umi dev server on https://localhost:8000 behind e2e/start-dev.mjs,
 *      which also exposes the http readiness endpoint polled here (Playwright's
 *      url probe cannot ignore the self-signed certificate).
 */

import { defineConfig } from '@playwright/test';

const backendPort = process.env.ADMIN_E2E_BACKEND_PORT ?? '18080';
const appPort = process.env.ADMIN_E2E_APP_PORT ?? '8000';
const devReadyPort = process.env.ADMIN_E2E_DEV_READY_PORT ?? '18081';

export default defineConfig({
  testDir: './e2e',
  outputDir: './e2e/results',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: `https://localhost:${appPort}`,
    ignoreHTTPSErrors: true,
    // Full Chromium in the new headless mode. Skipping `channel` would make
    // Playwright fetch the separate headless-shell build, which is one more
    // download for exactly the same coverage.
    channel: 'chromium',
    locale: 'zh-CN',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    actionTimeout: 10_000,
  },
  globalSetup: './e2e/global-setup.mjs',
  webServer: [
    {
      command: 'node e2e/start-backend.mjs',
      url: `http://127.0.0.1:${backendPort}/health/ready`,
      reuseExistingServer: !process.env.CI,
      timeout: 180_000,
    },
    {
      command: 'node e2e/start-dev.mjs',
      url: `http://127.0.0.1:${devReadyPort}/`,
      reuseExistingServer: !process.env.CI,
      timeout: 300_000,
    },
  ],
});
