/**
 * Order-detail status-driven actions. Only the server's `order.status` decides
 * which actions are available; a stale client view is corrected by a 409
 * presentation.
 */

import type { Order, OrderStatus } from "../../services/types.ts";

export type OrderAction = "request_refund" | "confirm_receipt";

export const ORDER_STATUS_TEXT: Record<OrderStatus, string> = {
  paid: "待发货",
  shipped: "待收货",
  completed: "已完成",
  refund_requested: "退款处理中",
  refunded: "已退款",
};

export function orderStatusTitle(status: OrderStatus): string {
  return ORDER_STATUS_TEXT[status] ?? status;
}

export function actionsForOrder(order: Order): OrderAction[] {
  const actions: OrderAction[] = [];
  if (order.status === "paid") actions.push("request_refund");
  if (order.status === "shipped") actions.push("confirm_receipt");
  return actions;
}

export function canRequestRefund(order: Order): boolean {
  return order.status === "paid";
}

export function canConfirmReceipt(order: Order): boolean {
  return order.status === "shipped";
}

export function hasRefundNote(order: Order): boolean {
  return order.status === "refund_requested" || order.status === "refunded";
}