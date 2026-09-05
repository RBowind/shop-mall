/**
 * Order list page (subpackage). Requires login. Order status, totals and item
 * data are all server-owned.
 */

import { ScrollView, Text, View } from "@tarojs/components";
import { useDidShow } from "@tarojs/taro";
import { useState } from "react";

import { OrderCard } from "../../components/order-card/order-card";
import { describeApiError, withTraceId } from "../../lib/errors";
import { getTaro } from "../../lib/taro";
import { listOrders } from "../../services/orders";
import type { Order, OrderStatus } from "../../services/types";
import { authStore } from "../../stores/auth";
import "./index.css";

type StatusFilter = OrderStatus | "";

const FILTER_TABS: Array<{ value: StatusFilter; label: string }> = [
  { value: "", label: "全部" },
  { value: "paid", label: "待发货" },
  { value: "shipped", label: "已发货" },
  { value: "completed", label: "已完成" },
  { value: "refund_requested", label: "退款申请中" },
  { value: "refunded", label: "已退款" },
];

export function OrdersPage() {
  const [orders, setOrders] = useState<Order[]>([]);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("");
  const [loading, setLoading] = useState(false);
  const [errorText, setErrorText] = useState("");

  const loadOrders = (filter: StatusFilter) => {
    setLoading(true);
    setErrorText("");
    listOrders({ status: filter === "" ? undefined : filter })
      .then((list) => {
        setOrders(list);
        setLoading(false);
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setLoading(false);
        setErrorText(withTraceId(presentation));
      });
  };

  useDidShow(() => {
    if (!authStore.requireLogin()) return;
    loadOrders(statusFilter);
  });

  const handleFilter = (filter: StatusFilter) => {
    setStatusFilter(filter);
    loadOrders(filter);
  };

  return (
    <View className="page orders-page">
      <ScrollView className="orders-tabs" scrollX enhanced showScrollbar={false}>
        <View className="orders-tabs__inner">
          {FILTER_TABS.map((tab) => (
            <Text
              key={tab.value}
              className={
                tab.value === statusFilter
                  ? "orders-tabs__tab orders-tabs__tab--active"
                  : "orders-tabs__tab"
              }
              onClick={() => handleFilter(tab.value)}
            >
              {tab.label}
            </Text>
          ))}
        </View>
      </ScrollView>
      {errorText ? <View className="error">{errorText}</View> : null}
      {loading && orders.length === 0 ? (
        <View className="loading-block">
          <t-loading theme="circular" size="48rpx" text="加载中…" />
        </View>
      ) : null}
      {orders.map((order) => (
        <OrderCard
          key={order.id}
          order={order}
          onTap={(target) => {
            getTaro().navigateTo({
              url: `/pages/order-detail/index?orderId=${encodeURIComponent(target.id)}`,
            });
          }}
        />
      ))}
      {!loading && orders.length === 0 && !errorText ? (
        <View className="empty-block">
          <t-empty description="该状态下暂无订单" />
        </View>
      ) : null}
    </View>
  );
}

export default OrdersPage;
