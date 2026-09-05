/**
 * Checkout idempotency-token lifecycle.
 *
 * The server keys order creation on the `Idempotency-Key` header plus a
 * request hash it computes from the selected cart item ids, the per-item
 * product/quantity pairs and the server-side address snapshot. The client
 * therefore reuses ONE token across every retry of the SAME checkout intent
 * (timeout, lost response, double tap) and mints a NEW token only when the
 * intent actually changes: cart lines, quantities or the selected address
 * (including its version, so an edited address does not replay into a 409).
 *
 * The token and the exact request payload are persisted so a retry can resend
 * the same cart item ids and address id even after the cart store refreshed
 * and consumed them.
 */

import {
  getPreferencesItem,
  removePreferencesItem,
  setPreferencesItem,
} from "./preferences.ts";
import { newClientToken } from "./uuid.ts";

export interface CheckoutLine {
  /** Cart item id (int64 serialized as string). */
  id: string;
  quantity: number;
}

export interface CheckoutIntent {
  lines: CheckoutLine[];
  addressId: string;
  addressVersion?: string;
}

export interface CheckoutSessionRecord {
  token: string;
  /** Client-side intent fingerprint; guards token reuse. */
  fingerprint: string;
  /** Exact request payload persisted for lost-response retries. */
  cartItemIds: string[];
  addressId: string;
}

const CHECKOUT_SESSION_KEY = "checkout.session.v1";

export function fingerprintOfIntent(intent: CheckoutIntent): string {
  const lines = [...intent.lines]
    .map((line) => ({ id: line.id, quantity: line.quantity }))
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  return JSON.stringify({
    lines,
    addressId: intent.addressId,
    addressVersion: intent.addressVersion ?? null,
  });
}

export function readCheckoutSession(): CheckoutSessionRecord | null {
  const raw = getPreferencesItem(CHECKOUT_SESSION_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as CheckoutSessionRecord;
    if (
      typeof parsed.token === "string" &&
      parsed.token !== "" &&
      Array.isArray(parsed.cartItemIds) &&
      typeof parsed.addressId === "string"
    ) {
      return parsed;
    }
    return null;
  } catch {
    return null;
  }
}

/**
 * Returns the token for a checkout intent, minting and persisting a new one
 * only when the persisted intent fingerprint differs.
 */
export function getCheckoutToken(intent: CheckoutIntent): CheckoutSessionRecord {
  const fingerprint = fingerprintOfIntent(intent);
  const existing = readCheckoutSession();
  if (existing && existing.fingerprint === fingerprint) {
    return existing;
  }
  const next: CheckoutSessionRecord = {
    token: newClientToken(),
    fingerprint,
    cartItemIds: intent.lines.map((line) => line.id),
    addressId: intent.addressId,
  };
  setPreferencesItem(CHECKOUT_SESSION_KEY, JSON.stringify(next));
  return next;
}

/** Drops the persisted checkout session (used after a conflict/abandonment). */
export function clearCheckoutSession(): void {
  removePreferencesItem(CHECKOUT_SESSION_KEY);
}