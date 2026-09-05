/**
 * Order detail page (subpackage). Requires login. Status-driven actions:
 * request refund (paid), confirm receipt (shipped). 404 (not owned/gone) and
 * 409 (status already changed) are presented distinctly with the trace ID.
 */

import { Image, Text, View } from "@tarojs/components";
import { useLoad } from "@tarojs/taro";
import { useEffect, useState } from "react";

import { orderStatusText } from "../../components/order-card/index";
import { describeApiError, withTraceId } from "../../lib/errors";
import { toLocalImageUrl } from "../../lib/image-url";
import { mulIntString } from "../../lib/points";
import { getTaro } from "../../lib/taro";
import { showErrorModal } from "../../lib/ui";
import { confirmOrder, getOrder, requestRefund } from "../../services/orders";
import type { Order } from "../../services/types";
import { actionsForOrder } from "./controller";
import "./index.css";

const REFUND_REASONS = ["不想要了", "拍错了，重新下单", "其他原因"];

/** Formats an ISO timestamp as "YYYY-MM-DD HH:mm" in local time; empty if absent. */
function formatTime(iso: string | null): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => (n < 10 ? `0${n}` : String(n));
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** Status → accent tone used by the detail header and pills. */
const STATUS_TONE: Record<string, string> = {
  paid: "action",
  shipped: "action",
  completed: "muted",
  refund_requested: "warn",
  refunded: "error",
};

export function OrderDetailPage() {
  const [orderId, setOrderId] = useState("");
  const [order, setOrder] = useState<Order | null>(null);
  const [loading, setLoading] = useState(false);
  const [errorText, setErrorText] = useState("");

  useLoad<{ orderId?: string }>((options) => {
    setOrderId(options.orderId ?? "");
  });

  useEffect(() => {
    if (!orderId) return;
    setLoading(true);
    setErrorText("");
    getOrder(orderId)
      .then((detail) => {
        setOrder(detail);
        setLoading(false);
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setLoading(false);
        setErrorText(withTraceId(presentation));
      });
  }, [orderId]);

  const actions = order ? actionsForOrder(order) : [];
  // Only reserve footer height when an action bar actually renders; a
  // completed/refunded order has no actions and gets no empty bottom block.
  const pageClass = actions.length > 0
    ? "page page--with-footer order-detail-page"
    : "page order-detail-page";

  const handleRequestRefund = () => {
    if (!order) return;
    getTaro().showActionSheet?.({
      itemList: REFUND_REASONS,
      success: (result) => {
        const reason = REFUND_REASONS[result.tapIndex];
        if (reason) submitRefund(reason);
      },
    });
  };

  const submitRefund = (reason: string) => {
    if (!order) return;
    requestRefund(order.id, reason)
      .then((updated) => {
        setOrder(updated);
        getTaro().showToast({ title: "退款申请已提交", icon: "success" });
      })
      .catch((err: unknown) => {
        showErrorModal(err);
      });
  };

  const handleConfirmReceipt = () => {
    if (!order) return;
    confirmOrder(order.id)
      .then((updated) => {
        setOrder(updated);
        getTaro().showToast({ title: "已确认收货", icon: "success" });
      })
      .catch((err: unknown) => {
        showErrorModal(err);
      });
  };

  return (
    <View className={pageClass}>
      {errorText ? <View className="error">{errorText}</View> : null}
      {loading && !order ? (
        <View className="loading-block">
          <t-loading theme="circular" size="48rpx" text="加载中…" />
        </View>
      ) : null}
      {order ? (
        <>
          <View className="od-status-card">
            <Text
              className={`od-status-card__title od-status-card__title--${STATUS_TONE[order.status] ?? "muted"}`}
            >
              {orderStatusText(order.status)}
            </Text>
            <Text className="od-status-card__time">
              下单时间 {formatTime(order.created_at)}
            </Text>
          </View>
          <View className="od-address-card">
            <t-icon name="location" size="44rpx" color="#0052d9" />
            <View className="od-address-card__body">
              <Text className="od-address-card__receiver">
                {order.receiver} {order.phone}
              </Text>
              <Text className="od-address-card__detail">{order.address}</Text>
            </View>
          </View>
          {order.refund_reason ? (
            <View className="od-notice">退款原因：{order.refund_reason}</View>
          ) : null}
          {order.refund_reject_reason ? (
            <View className="od-notice od-notice--danger">
              退款被拒：{order.refund_reject_reason}
            </View>
          ) : null}
          <View className="od-card">
            <Text className="od-card__title">商品信息</Text>
            {order.items.map((item) => (
              <View
                key={`${item.product_id}-${item.price_snapshot}`}
                className="od-item"
              >
                <View className="od-item__thumb-wrap">
                  <Image
                    className="od-item__image"
                    src={toLocalImageUrl(item.product_image)}
                    mode="aspectFill"
                  />
                </View>
                <View className="od-item__body">
                  <Text className="od-item__name">{item.product_name}</Text>
                  <View className="od-item__meta">
                    <Text className="od-item__unit">
                      {item.price_snapshot} 积分 × {item.quantity}
                    </Text>
                    <Text className="od-item__subtotal">
                      {mulIntString(item.price_snapshot, item.quantity)} 积分
                    </Text>
                  </View>
                </View>
              </View>
            ))}
            <View className="od-total-row">
              <Text className="od-total-row__label">合计</Text>
              <Text className="od-total-row__value">
                {order.total_points} <Text className="od-total-row__unit">积分</Text>
              </Text>
            </View>
          </View>
          <View className="od-cell-group">
            <View className="od-cell">
              <Text className="od-cell__label">订单编号</Text>
              <Text className="od-cell__value od-cell__value--mono">
                {order.order_no}
              </Text>
            </View>
            {order.shipped_at ? (
              <View className="od-cell">
                <Text className="od-cell__label">发货时间</Text>
                <Text className="od-cell__value">{formatTime(order.shipped_at)}</Text>
              </View>
            ) : null}
            {order.completed_at ? (
              <View className="od-cell">
                <Text className="od-cell__label">完成时间</Text>
                <Text className="od-cell__value">
                  {formatTime(order.completed_at)}
                </Text>
              </View>
            ) : null}
            {order.refunded_at ? (
              <View className="od-cell">
                <Text className="od-cell__label">退款时间</Text>
                <Text className="od-cell__value">{formatTime(order.refunded_at)}</Text>
              </View>
            ) : null}
          </View>
          {actions.length > 0 ? (
            <View className="od-footer">
              {actions.includes("request_refund") ? (
                <t-button
                  theme="danger"
                  variant="outline"
                  size="large"
                  onTap={() => handleRequestRefund()}
                >
                  申请退款
                </t-button>
              ) : null}
              {actions.includes("confirm_receipt") ? (
                <t-button
                  theme="primary"
                  size="large"
                  onTap={() => handleConfirmReceipt()}
                >
                  确认收货
                </t-button>
              ) : null}
            </View>
          ) : null}
        </>
      ) : null}
    </View>
  );
}

export default OrderDetailPage;
