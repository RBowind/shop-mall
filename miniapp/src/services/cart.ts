/**
 * Buyer cart service. The server owns quantities, stock and the purchasable
 * flag (a cart item whose product went off sale stays visible but is not
 * purchasable).
 */

import { getTransport } from "../lib/transport.ts";
import type { CartItem, CartItemResponse, CartResponse, EmptyResponse } from "./types.ts";

export async function getCart(): Promise<CartItem[]> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/v1/cart",
  });
  const envelope = response.data as CartResponse;
  return envelope.data.list;
}

export async function addCartItem(
  productId: string,
  quantity: number,
): Promise<CartItem> {
  const response = await getTransport()({
    method: "POST",
    path: "/api/v1/cart",
    body: { product_id: productId, quantity },
  });
  return (response.data as CartItemResponse).data;
}

export async function updateCartItem(
  itemId: string,
  quantity: number,
): Promise<CartItem> {
  const response = await getTransport()({
    method: "PATCH",
    path: `/api/v1/cart/${encodeURIComponent(itemId)}`,
    body: { quantity },
  });
  return (response.data as CartItemResponse).data;
}

export async function deleteCartItem(itemId: string): Promise<void> {
  await getTransport()({
    method: "DELETE",
    path: `/api/v1/cart/${encodeURIComponent(itemId)}`,
  });
}