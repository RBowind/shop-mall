/**
 * Singleton API transport for the admin console.
 *
 * The app wires the real transport once at startup (`src/app.ts` boot), and
 * tests install a fake transport with `configureTransport` before exercising
 * services. Services call `getTransport()` and never construct URLs, headers
 * or cookies themselves; CSRF injection and the 401 redirect live in
 * `createAdminTransport`.
 */

import type { ApiResponse, ApiTransport } from "./generated/api";
import { createAdminTransport } from "./request.ts";

let transport: ApiTransport | null = null;

export function getTransport(): ApiTransport {
  if (!transport) {
    throw new Error("admin transport is not configured");
  }
  return transport;
}

export function configureTransport(next: ApiTransport): void {
  transport = next;
}

export interface AppTransportOptions {
  baseUrl?: string;
  redirectToLogin?: () => void;
}

export function createAppTransport(options: AppTransportOptions = {}): ApiTransport {
  return createAdminTransport({
    baseUrl: options.baseUrl ?? "",
    redirectToLogin: options.redirectToLogin,
  });
}

/** Unwraps the envelope `{ code, data, message, trace_id }` returned by the API. */
export function unwrap<T>(response: ApiResponse): T {
  const envelope = response.data as { data?: T } | undefined;
  return envelope?.data as T;
}

/** Normalizes the paginated envelope shape `{ list?, total, page, page_size }`. */
export function parsePage<T>(data: {
  list?: T[] | undefined;
  total?: number | undefined;
  page?: number | undefined;
  page_size?: number | undefined;
}): { list: T[]; total: number; page: number; page_size: number } {
  return {
    list: data.list ?? [],
    total: data.total ?? 0,
    page: data.page ?? 1,
    page_size: data.page_size ?? 0,
  };
}