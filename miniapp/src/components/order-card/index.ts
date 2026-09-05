/**
 * Order card. Displays server-owned order data (order number, status, totals)
 * and emits a tap event for the host page to open the detail page.
 */

import type { MiniComponentDefinition } from "../../lib/taro.ts";
import type { Order, OrderStatus } from "../../services/types.ts";

const STATUS_TEXT: Record<OrderStatus, string> = {
  paid: "待发货",
  shipped: "已发货",
  completed: "已完成",
  refund_requested: "退款申请中",
  refunded: "已退款",
};

export function orderStatusText(status: OrderStatus): string {
  return STATUS_TEXT[status] ?? status;
}

export interface OrderCardVM {
  orderNo: string;
  statusText: string;
  totalText: string;
  totalValue: string;
  itemCount: number;
  firstItemName: string;
  firstUnitPoints: string;
  firstQuantity: number;
}

export function toOrderCardVM(order: Order): OrderCardVM {
  const first = order.items[0];
  return {
    orderNo: order.order_no,
    statusText: orderStatusText(order.status),
    totalText: `${order.total_points} 积分`,
    totalValue: order.total_points,
    itemCount: order.items.length,
    firstItemName: first?.product_name ?? "",
    firstUnitPoints: first?.price_snapshot ?? "",
    firstQuantity: first?.quantity ?? 0,
  };
}

export interface OrderCardHandlers {
  onTap(order: Order): void;
}

export function createOrderCardComponent(
  handlers: OrderCardHandlers,
): MiniComponentDefinition {
  return {
    name: "order-card",
    properties: { order: { type: Object } },
    data: {},
    methods: {
      onTap(this: unknown) {
        const order = (this as { order?: Order }).order;
        if (order) handlers.onTap(order);
      },
    },
  };
}