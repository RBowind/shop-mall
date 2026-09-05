/**
 * Buyer order service, including the checkout idempotency contract.
 *
 * `createOrder` never mints a token itself: it delegates to the persisted
 * checkout session so every retry of the same intent reuses the SAME
 * `Idempotency-Key` and the SAME cart item ids / address id, and a new token
 * is minted only when the intent fingerprint changes (cart lines, quantities
 * or selected address). The server is the authority on replay vs conflict.
 */

import {
  clearCheckoutSession,
  getCheckoutToken,
  type CheckoutIntent,
  type CheckoutLine,
} from "../lib/checkout-session.ts";
import { getTransport } from "../lib/transport.ts";
import type {
  Order,
  OrderListResponse,
  OrderResponse,
  OrderStatus,
} from "./types.ts";

export type { CheckoutIntent, CheckoutLine } from "../lib/checkout-session.ts";
export { clearCheckoutSession } from "../lib/checkout-session.ts";

export interface OrderListQuery {
  page?: number;
  page_size?: number;
  status?: OrderStatus;
}

export async function createOrder(intent: CheckoutIntent): Promise<Order> {
  const session = getCheckoutToken(intent);
  const response = await getTransport()({
    method: "POST",
    path: "/api/v1/orders",
    headers: { "Idempotency-Key": session.token },
    body: {
      cart_item_ids: session.cartItemIds,
      address_id: session.addressId,
    },
  });
  return (response.data as OrderResponse).data;
}

export async function listOrders(
  query: OrderListQuery = {},
): Promise<Order[]> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/v1/orders",
    query: {
      page: query.page,
      page_size: query.page_size,
      status: query.status,
    },
  });
  const envelope = response.data as OrderListResponse;
  return envelope.data.list ?? [];
}

export async function getOrder(orderId: string): Promise<Order> {
  const response = await getTransport()({
    method: "GET",
    path: `/api/v1/orders/${encodeURIComponent(orderId)}`,
  });
  return (response.data as OrderResponse).data;
}

export async function requestRefund(
  orderId: string,
  reason: string,
): Promise<Order> {
  const response = await getTransport()({
    method: "POST",
    path: `/api/v1/orders/${encodeURIComponent(orderId)}/refund`,
    body: { reason },
  });
  return (response.data as OrderResponse).data;
}

export async function confirmOrder(orderId: string): Promise<Order> {
  const response = await getTransport()({
    method: "POST",
    path: `/api/v1/orders/${encodeURIComponent(orderId)}/confirm`,
  });
  return (response.data as OrderResponse).data;
}