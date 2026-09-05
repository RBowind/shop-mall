/**
 * Administrator refund service.
 *
 * The refund list reuses the order list response shape (each refund request is
 * an order in `refund_requested` state). Approve and reject are gated by the
 * refund:approve permission on the backend; the frontend only shows the
 * actions when the session carries it.
 */

import { getTransport, parsePage, unwrap } from "./transport.ts";
import type { components } from "./generated/api";
import type { Order } from "./types.ts";

export interface RefundListQuery {
  page?: number;
  page_size?: number;
}

export interface RefundPage {
  list: Order[];
  total: number;
  page: number;
  page_size: number;
}

export async function listRefunds(
  query: RefundListQuery = {},
): Promise<RefundPage> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/admin/v1/refunds",
    query: { page: query.page, page_size: query.page_size },
  });
  const envelope = response.data as components["schemas"]["OrderListResponse"];
  return parsePage<Order>(envelope.data);
}

export async function approveRefund(orderId: string): Promise<Order> {
  const response = await getTransport()({
    method: "POST",
    path: `/api/admin/v1/refunds/${encodeURIComponent(orderId)}/approve`,
  });
  return unwrap<Order>(response);
}

export async function rejectRefund(
  orderId: string,
  reason: string,
): Promise<Order> {
  const response = await getTransport()({
    method: "POST",
    path: `/api/admin/v1/refunds/${encodeURIComponent(orderId)}/reject`,
    body: { reason },
  });
  return unwrap<Order>(response);
}