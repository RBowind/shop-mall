/**
 * Singleton API transport for the miniapp.
 *
 * The app wires token injection and 401 session cleanup once at startup
 * (`src/app.ts`). Services call `getTransport()` and never construct URLs or
 * headers themselves. Tests install a fake transport with
 * `configureTransport` before exercising services.
 */

import type { ApiTransport } from "../services/generated/api.ts";
import { createMiniappTransport } from "../services/request.ts";

let transport: ApiTransport | null = null;

export function getTransport(): ApiTransport {
  if (!transport) {
    throw new Error("miniapp transport is not configured");
  }
  return transport;
}

export function configureTransport(next: ApiTransport): void {
  transport = next;
}

export function createAppTransport(
  baseUrl: string,
  getAccessToken: () => string | null | undefined,
  clearSession: () => void,
): ApiTransport {
  return createMiniappTransport({ baseUrl, getAccessToken, clearSession });
}