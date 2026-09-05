/**
 * Playwright webServer launcher for the admin dev server.
 *
 * Runs `max dev` with ADMIN_E2E=1 (which flips .umirc.ts to https + /api
 * proxy), waits until the self-signed https dev server answers, then exposes a
 * plain-http readiness endpoint on DEV_READY_PORT for Playwright to poll
 * (Playwright's own url check cannot be told to ignore TLS errors).
 *
 * NODE_TLS_REJECT_UNAUTHORIZED is scoped to this launcher process only: the
 * dev server certificate is committed test infrastructure, not a secret.
 */

import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import process from 'node:process';

import { APP_PORT, DEV_READY_PORT } from './lib/env.mjs';

process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
process.env.ADMIN_E2E = '1';
process.env.BROWSER = 'none';
process.env.PORT = String(APP_PORT);

const child = spawn('pnpm', ['exec', 'max', 'dev'], {
  cwd: process.cwd(),
  env: process.env,
  stdio: ['ignore', 'inherit', 'inherit'],
  shell: process.platform === 'win32',
});

let ready = false;
const deadline = Date.now() + 5 * 60 * 1000;
(async () => {
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      console.error('[e2e dev] max dev exited before becoming ready');
      process.exit(1);
    }
    try {
      const response = await fetch(`https://localhost:${APP_PORT}/`, {
        redirect: 'manual',
      });
      if (response.status > 0) {
        ready = true;
        console.error(`[e2e dev] dev server ready on https://localhost:${APP_PORT}`);
        return;
      }
    } catch {
      // not up yet
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  console.error('[e2e dev] dev server did not become ready within 5 minutes');
  killAll();
  process.exit(1);
})();

const readyServer = createServer((request, response) => {
  if (ready) {
    response.writeHead(200, { 'Content-Type': 'text/plain' });
    response.end('dev-server-ready');
  } else {
    response.writeHead(503);
    response.end('not-ready');
  }
});
readyServer.listen(DEV_READY_PORT, '127.0.0.1');

function killAll() {
  if (process.platform === 'win32' && child.pid) {
    spawn('taskkill', ['/pid', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  } else {
    child.kill('SIGTERM');
  }
  readyServer.close();
}
process.on('SIGTERM', killAll);
process.on('SIGINT', killAll);
child.on('exit', () => {
  readyServer.close();
  process.exit(0);
});
