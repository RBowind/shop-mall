/**
 * Administrator points service.
 *
 * Every adjustment is written through the generated transport with a fresh
 * Idempotency-Key; the backend treats the key as part of the server-generated
 * ledger event key, so re-submitting the same form with a new key is a new
 * adjustment and replaying the same key returns the original ledger entry.
 * The remark is mandatory and validated by the backend (422 otherwise).
 */

import { getTransport, unwrap } from "./transport.ts";
import { newIdempotencyKey } from "../lib/idem.ts";
import type { components } from "./generated/api";
import type { LedgerEntry } from "./types.ts";

export interface PointsAdjustInput {
  /** Buyer user id, kept as a string (int64). */
  user_id: string;
  /** Signed delta, kept as a string (int64), e.g. "-100" or "50". */
  delta: string;
  /** Mandatory remark recorded in the ledger entry. */
  remark: string;
}

export async function adjustPoints(
  input: PointsAdjustInput,
  idempotencyKey?: string,
): Promise<LedgerEntry> {
  const key = idempotencyKey ?? newIdempotencyKey();
  const response = await getTransport()({
    method: "POST",
    path: "/api/admin/v1/points/adjust",
    headers: { "Idempotency-Key": key },
    body: {
      user_id: input.user_id,
      delta: input.delta,
      remark: input.remark,
    },
  });
  return unwrap<LedgerEntry>(response);
}