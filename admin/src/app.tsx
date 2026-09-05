/**
 * Umi Max app bootstrap.
 *
 * - `getInitialState` loads the current administrator via `/auth/me` and widens
 *   an empty permission list from the roles endpoint. A failure (no session or
 *   expired cookie) degrades to `{ session: null }`, which makes every
 *   permission bit false and sends the layout to the login page.
 * - The singleton transport is wired once at module load: the CSRF / cookie /
 *   credentials logic lives in `services/request.ts`, and any 401 routes the
 *   user back to `/user/login` with `?reason=session`.
 */

import { history } from '@umijs/max';

import { configureTransport, createAppTransport } from './services/transport.ts';
import { me, withPermissions } from './services/admin.ts';
import type { AdminSession } from './services/admin.ts';
import { listRoles } from './services/access.ts';

function readApiBase(): string {
  const configured = (
    window as unknown as { SHOP_MALL_ADMIN_API_BASE?: string }
  ).SHOP_MALL_ADMIN_API_BASE;
  return typeof configured === "string" ? configured : "";
}

// Wire the singleton transport before any service call. The 401 handler keeps
// the reason query so the login page can show the stale-session message.
configureTransport(
  createAppTransport({
    baseUrl: readApiBase(),
    redirectToLogin: () => {
      history.replace('/user/login?reason=session');
    },
  }),
);

export interface AdminInitialState {
  session: AdminSession | null;
}

export async function getInitialState(): Promise<AdminInitialState> {
  // The boot probe is pointless on the login page and races a fast (or
  // autofilled) sign-in: its late 401 resolves after the login page already
  // stored the session and clobbers it back to null, bouncing the user to
  // /user/login?reason=session. Skip it there.
  if (typeof window !== 'undefined' && window.location.hash.includes('/user/login')) {
    return { session: null };
  }
  try {
    const profile = await me();
    const session = await withPermissions(profile, listRoles);
    return { session };
  } catch {
    // No valid session: transport has already redirected on 401; fall back to
    // an unauthenticated state for first visits.
    return { session: null };
  }
}