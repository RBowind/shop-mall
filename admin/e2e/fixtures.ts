/**
 * Fixtures for the admin E2E specs.
 *
 * `adminPage` / `viewerPage` authenticate through the real login API inside
 * the browser context (context.request shares the cookie jar with pages), so
 * page specs skip the login UI unless the UI flow itself is under test.
 */

import { readFileSync } from 'node:fs';
import path from 'node:path';

import { test as base, expect } from '@playwright/test';
import type { BrowserContext, Page } from '@playwright/test';

interface SeedCredentials {
  username: string;
  password: string;
}

export interface SeedState {
  superAdmin: SeedCredentials;
  viewer: SeedCredentials;
  buyerToken: string;
  products: {
    onSale: { id: string; name: string };
    orderable: { id: string; name: string };
    offSale: { id: string; name: string };
  };
  order: { id: string; orderNo: string };
}

// Specs transpile to CJS (no "type": "module" in package.json), so no
// import.meta here; resolve from the admin package root, which is the cwd of
// every `playwright test` invocation via `pnpm test:e2e`.
function seedStatePath(): string {
  return path.resolve(process.cwd(), 'e2e', '.build', 'seed-state.json');
}

export function loadSeedState(): SeedState {
  return JSON.parse(readFileSync(seedStatePath(), 'utf8')) as SeedState;
}

const appOrigin = `https://localhost:${process.env.ADMIN_E2E_APP_PORT ?? '8000'}`;

async function apiLogin(context: BrowserContext, credentials: SeedCredentials): Promise<void> {
  const response = await context.request.post('/api/admin/v1/auth/login', {
    data: { username: credentials.username, password: credentials.password },
    headers: { Origin: appOrigin },
  });
  if (!response.ok()) {
    throw new Error(`api login for ${credentials.username} failed: ${response.status()}`);
  }
}

export const test = base.extend<{
  seed: SeedState;
  adminContext: BrowserContext;
  adminPage: Page;
  viewerContext: BrowserContext;
  viewerPage: Page;
}>({
  seed: async ({}, use) => {
    await use(loadSeedState());
  },
  adminContext: async ({ browser }, use) => {
    const context = await browser.newContext();
    await apiLogin(context, loadSeedState().superAdmin);
    await use(context);
    await context.close();
  },
  adminPage: async ({ adminContext }, use) => {
    const page = await adminContext.newPage();
    await use(page);
  },
  viewerContext: async ({ browser }, use) => {
    const context = await browser.newContext();
    await apiLogin(context, loadSeedState().viewer);
    await use(context);
    await context.close();
  },
  viewerPage: async ({ viewerContext }, use) => {
    const page = await viewerContext.newPage();
    await use(page);
  },
});

export { expect };
