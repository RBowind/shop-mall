/**
 * Taro/React render for the order card. Displays the server-owned order
 * number, a status pill, the first line item (image, name, unit points ×
 * quantity), the points total and a detail chip; emits a tap event for
 * the parent page to open the detail page.
 */

import { Image, Text, View } from "@tarojs/components";

import { toLocalImageUrl } from "../../lib/image-url";
import type { Order } from "../../services/types";
import { toOrderCardVM } from "./index";
import "./order-card.css";

/** Maps each server status to the status-pill tone on the card. */
const STATUS_PILL_TONE: Record<string, string> = {
  paid: "action",
  shipped: "action",
  completed: "muted",
  refund_requested: "warn",
  refunded: "error",
};

export interface OrderCardProps {
  order: Order;
  onTap(order: Order): void;
}

export function OrderCard({ order, onTap }: OrderCardProps) {
  const vm = toOrderCardVM(order);
  const first = order.items[0];
  const tone = STATUS_PILL_TONE[order.status] ?? "muted";
  return (
    <View className="order-card" onClick={() => onTap(order)}>
      <View className="order-card__head">
        <Text className="order-card__no">订单号 {vm.orderNo}</Text>
        <Text className={`order-card__pill order-card__pill--${tone}`}>
          {vm.statusText}
        </Text>
      </View>
      {first ? (
        <View className="order-card__item">
          <View className="order-card__thumb-wrap">
            <Image
              className="order-card__thumb"
              src={toLocalImageUrl(first.product_image)}
              mode="aspectFill"
            />
          </View>
          <View className="order-card__item-main">
            <Text className="order-card__name">{vm.firstItemName}</Text>
            <Text className="order-card__unit">
              {vm.firstUnitPoints} 积分 × {vm.firstQuantity}
            </Text>
          </View>
          {vm.itemCount > 1 ? (
            <Text className="order-card__count">共 {vm.itemCount} 件</Text>
          ) : null}
        </View>
      ) : null}
      <View className="order-card__foot">
        <Text className="order-card__total">
          合计 <Text className="order-card__total-num">{vm.totalValue}</Text> 积分
        </Text>
        <Text className="order-card__detail">查看详情 ›</Text>
      </View>
    </View>
  );
}
