/**
 * Administrator order service.
 *
 * `shipOrder` is gated by the order:ship permission on the backend; the
 * frontend only shows the action when the session carries it. Refund
 * review lives in `services/refunds.ts`.
 */

import { getTransport, parsePage, unwrap } from "./transport.ts";
import type { components } from "./generated/api";
import type { Order, OrderStatus } from "./types.ts";

export interface OrderListQuery {
  page?: number;
  page_size?: number;
  status?: OrderStatus;
}

export interface OrderPage {
  list: Order[];
  total: number;
  page: number;
  page_size: number;
}

export async function listOrders(
  query: OrderListQuery = {},
): Promise<OrderPage> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/admin/v1/orders",
    query: { page: query.page, page_size: query.page_size, status: query.status },
  });
  const envelope = response.data as components["schemas"]["OrderListResponse"];
  return parsePage<Order>(envelope.data);
}

export async function getOrder(orderId: string): Promise<Order> {
  const response = await getTransport()({
    method: "GET",
    path: `/api/admin/v1/orders/${encodeURIComponent(orderId)}`,
  });
  return unwrap<Order>(response);
}

export async function shipOrder(orderId: string): Promise<Order> {
  const response = await getTransport()({
    method: "POST",
    path: `/api/admin/v1/orders/${encodeURIComponent(orderId)}/ship`,
  });
  return unwrap<Order>(response);
}