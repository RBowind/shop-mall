/**
 * Client-generated idempotency token (UUID v4).
 *
 * The token is the client side of the checkout idempotency contract: it must
 * be unique per checkout intent and stable across retries of the same intent.
 * It is not a security credential, so a deterministic fallback is acceptable
 * when the runtime provides no crypto source.
 */

function randomBytes(length: number): Uint8Array {
  const g = globalThis as {
    crypto?: { getRandomValues?(array: Uint8Array): Uint8Array };
  };
  const bytes = new Uint8Array(length);
  if (g.crypto?.getRandomValues) {
    return g.crypto.getRandomValues(bytes);
  }
  for (let i = 0; i < length; i += 1) {
    bytes[i] = Math.floor(Math.random() * 256);
  }
  return bytes;
}

export function newClientToken(): string {
  const bytes = randomBytes(16);
  bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
  bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10xx
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20),
  ].join("-");
}