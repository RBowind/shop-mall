/**
 * Idempotency key generation for administrator writes.
 *
 * The backend treats the Idempotency-Key as part of the server-generated
 * ledger event key for points adjustments, so a fresh, unique key is required
 * per submit. `crypto.randomUUID` exists in browsers served over HTTPS and in
 * Node 24; the fallback keeps the key unique enough for browsers served over
 * plain HTTP.
 */

export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  const random = Math.random().toString(36).slice(2, 12);
  return `idem-${Date.now().toString(36)}-${random}`;
}