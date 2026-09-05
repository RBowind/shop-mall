/**
 * Minimal HTTP helpers for the Node-side seed script.
 *
 * Talks to the backend directly (BACKEND_URL), replaying the cookie + CSRF
 * handshake the browser transport performs: login sets `admin_access_token`
 * (HttpOnly) plus the readable `csrf_token`; every later write echoes the CSRF
 * cookie in X-CSRF-Token, and admin endpoints validate the Origin header.
 */

import { APP_URL, BACKEND_URL } from './env.mjs';

function parseCookies(response) {
  const jar = new Map();
  // Node 18+ fetch hides set-cookie behind getSetCookie() on some paths.
  const setter = response.headers.getSetCookie?.() ?? [];
  for (const line of setter) {
    const [pair] = line.split(';');
    const eq = pair.indexOf('=');
    if (eq > 0) jar.set(pair.slice(0, eq).trim(), pair.slice(eq + 1).trim());
  }
  return jar;
}

async function readEnvelope(response) {
  const body = await response.json().catch(() => null);
  if (!response.ok) {
    const message = body?.message ?? '<no message>';
    throw new Error(`${response.status} ${response.url}: ${message}`);
  }
  return body?.data ?? body;
}

/** Logs in an administrator and returns a session carrying Cookie + CSRF. */
export async function adminLogin(username, password) {
  const response = await fetch(`${BACKEND_URL}/api/admin/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Origin: APP_URL },
    body: JSON.stringify({ username, password }),
  });
  if (!response.ok) {
    const body = await response.text().catch(() => '');
    throw new Error(`admin login ${username} failed: ${response.status} ${body}`);
  }
  const jar = parseCookies(response);
  const csrf = jar.get('csrf_token');
  if (!csrf) throw new Error(`admin login ${username} did not set csrf_token cookie`);
  const cookie = [...jar.entries()].map(([k, v]) => `${k}=${v}`).join('; ');
  return { cookie, csrf };
}

export async function adminRequest(session, method, path, body) {
  const response = await fetch(`${BACKEND_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Origin: APP_URL,
      Cookie: session.cookie,
      ...(method === 'GET' ? {} : { 'X-CSRF-Token': session.csrf }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return readEnvelope(response);
}

/** Fake-client wx login: any non-empty code maps to the fixed openid buyer. */
export async function buyerLogin(code) {
  const response = await fetch(`${BACKEND_URL}/api/v1/auth/wx-login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code }),
  });
  const data = await readEnvelope(response);
  if (!data?.access_token) throw new Error('wx-login response missing access_token');
  return data;
}

export async function buyerRequest(token, method, path, body, extraHeaders = {}) {
  const response = await fetch(`${BACKEND_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
      ...extraHeaders,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return readEnvelope(response);
}
