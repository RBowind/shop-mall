/**
 * Buyer points ledger service (read-only).
 *
 * The ledger is append-only server-side; the miniapp never writes to it.
 * Every int64 field (id, delta, balance_after) stays a string per the
 * contract — presentation formats it with the helpers in `lib/points.ts`.
 */

import { getTransport } from "../lib/transport.ts";
import type { components } from "./generated/api.ts";

export type LedgerEntry = components["schemas"]["LedgerEntry"];
export type LedgerEntryType = LedgerEntry["type"];

type LedgerListEnvelope = components["schemas"]["LedgerListResponse"];

export interface LedgerListQuery {
  page?: number;
  page_size?: number;
}

export async function listPointsLedger(
  query: LedgerListQuery = {},
): Promise<LedgerEntry[]> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/v1/points/ledger",
    query: {
      page: query.page,
      page_size: query.page_size,
    },
  });
  const envelope = response.data as LedgerListEnvelope;
  return envelope.data.list ?? [];
}
