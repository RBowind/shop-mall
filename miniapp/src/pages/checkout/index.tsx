/**
 * Checkout page (subpackage). Requires login.
 *
 * Submits ONLY cart item ids + address id (never client-computed prices). The
 * order service reuses the persisted `Idempotency-Key` for the same intent and
 * mints a new one when the cart, quantities or address change. Network errors
 * and lost responses offer a retry that resends the exact same token and
 * payload; a 409 conflict clears the token and asks the user to re-confirm
 * against refreshed data.
 *
 * Visual follows the OpenDesign checkout prototype: one white 订单确认 card —
 * address line (name + phone + 默认 pill + detail, 更换 > on the right),
 * hairline, compact item lines, grey note, 积分合计 big red row — then the
 * full-width blue 确认兑换 button and the 积分余额 card. All submit logic is
 * untouched.
 */

import { Image, Text, View } from "@tarojs/components";
import { useDidShow } from "@tarojs/taro";
import { useState, useSyncExternalStore } from "react";

import { clearCheckoutSession, type CheckoutIntent } from "../../lib/checkout-session";
import { describeApiError, withTraceId } from "../../lib/errors";
import { toLocalImageUrl } from "../../lib/image-url";
import { compareIntString, sumCartItems } from "../../lib/points";
import { getTaro } from "../../lib/taro";
import { showErrorModal } from "../../lib/ui";
import { listAddresses } from "../../services/addresses";
import { getMe } from "../../services/auth";
import { createOrder } from "../../services/orders";
import type { Address } from "../../services/types";
import { authStore } from "../../stores/auth";
import { cartStore, purchasableItems } from "../../stores/cart";
import { buildCheckoutIntent } from "./controller";

import "./index.css";

export function CheckoutPage() {
  const [addresses, setAddresses] = useState<Address[]>([]);
  const [selectedAddressId, setSelectedAddressId] = useState("");
  const [balanceText, setBalanceText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [errorText, setErrorText] = useState("");
  const [submittedIntent, setSubmittedIntent] = useState<CheckoutIntent | null>(null);

  const items = useSyncExternalStore(
    cartStore.subscribe,
    () => cartStore.getState().items,
  );
  const purchasable = purchasableItems(items);
  const subtotal = sumCartItems(purchasable);
  const insufficient =
    balanceText !== "" &&
    subtotal !== "" &&
    compareIntString(balanceText, subtotal) < 0;

  useDidShow(() => {
    if (!authStore.requireLogin()) {
      getTaro().navigateBack();
      return;
    }
    const state = authStore.getState();
    setBalanceText(state.user?.points_balance ?? "");
    cartStore
      .refresh()
      .then(() => listAddresses())
      .then((list) => {
        setAddresses(list);
        setSelectedAddressId(
          list.find((address) => address.is_default)?.id ?? list[0]?.id ?? "",
        );
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setErrorText(withTraceId(presentation));
      });
    // Keep the balance fresh so the footer reflects the latest server state.
    getMe()
      .then((me) => setBalanceText(me.points_balance))
      .catch(() => {
        // balance is a hint; a stale value never blocks the submit path
      });
  });

  const submitIntent = (intent: CheckoutIntent) => {
    setSubmitting(true);
    setErrorText("");
    createOrder(intent)
      .then((order) => {
        setSubmitting(false);
        getTaro().redirectTo({
          url: `/pages/order-detail/index?orderId=${encodeURIComponent(order.id)}`,
        });
      })
      .catch((err: unknown) => {
        setSubmitting(false);
        const presentation = describeApiError(err);
        if (presentation.kind === "conflict") {
          clearCheckoutSession();
          getTaro().showModal({
            title: presentation.title,
            content: `${withTraceId(presentation)}\n已刷新，请再次确认下单`,
            confirmText: "重新确认",
            success: (result) => {
              if (result.confirm) submitCurrent();
            },
          });
        } else if (presentation.kind === "network") {
          getTaro().showModal({
            title: presentation.title,
            content: `${withTraceId(presentation)}\n是否重试？将使用同一订单号重试`,
            confirmText: "重试",
            success: (result) => {
              if (result.confirm) submitIntent(intent);
            },
          });
        } else {
          showErrorModal(err);
        }
      });
  };

  const submitCurrent = () => {
    const address = addresses.find((item) => item.id === selectedAddressId);
    if (!address) {
      getTaro().showToast({ title: "请先选择收货地址", icon: "none" });
      return;
    }
    const intent = buildCheckoutIntent(items, address);
    if (intent.lines.length === 0) {
      getTaro().showToast({ title: "没有可下单的商品", icon: "none" });
      return;
    }
    setSubmittedIntent(intent);
    submitIntent(intent);
  };

  const retrySubmit = () => {
    if (!submittedIntent) return;
    // Reuses the persisted token and the exact original payload.
    submitIntent(submittedIntent);
  };

  const goToAddressList = () => {
    getTaro().navigateTo({ url: "/pages/address/index" });
  };

  return (
    <View className="page">
      {errorText ? <View className="error">{errorText}</View> : null}
      <View className="ck-card">
        <Text className="ck-card__title">订单确认</Text>

        {addresses.length === 0 ? (
          <View className="empty-block">
            <t-empty description="暂无收货地址，请先到「我的-收货地址」添加" />
          </View>
        ) : (
          addresses.map((address) => {
            const active = address.id === selectedAddressId;
            return (
              <View
                key={address.id}
                className={active ? "ck-addr ck-addr--active" : "ck-addr"}
                onClick={() => setSelectedAddressId(address.id)}
              >
                <View className="ck-addr__icon">
                  <t-icon
                    name="location"
                    size="40rpx"
                    color={active ? "#0052d9" : "rgba(0,0,0,0.4)"}
                  />
                </View>
                <View className="ck-addr__lines">
                  <View className="ck-addr__receiver-row">
                    <Text className="ck-addr__receiver">
                      {address.receiver} {address.phone}
                    </Text>
                    {address.is_default ? (
                      <View className="ck-addr__default">默认</View>
                    ) : null}
                  </View>
                  <Text className="ck-addr__detail">
                    {address.region} {address.detail}
                  </Text>
                </View>
                {active ? (
                  <Text className="ck-addr__swap" onClick={goToAddressList}>
                    更换 &gt;
                  </Text>
                ) : null}
              </View>
            );
          })
        )}

        <View className="ck-hairline" />

        {purchasable.map((item) => (
          <View key={item.id} className="ck-item">
            <Image
              className="ck-item__image"
              src={toLocalImageUrl(item.product.main_image)}
              mode="aspectFill"
            />
            <View className="ck-item__body">
              <Text className="ck-item__name">{item.product.name}</Text>
              <Text className="ck-item__unit">
                单价：{item.product.price_points} 积分
              </Text>
            </View>
            <Text className="ck-item__qty">×{item.quantity}</Text>
          </View>
        ))}
        <Text className="ck-note">合计按所选商品计算</Text>

        <View className="ck-hairline" />

        <View className="ck-total">
          <Text className="ck-total__label">积分合计</Text>
          <Text className="ck-total__num">{subtotal}</Text>
          <Text className="ck-total__unit">积分</Text>
        </View>

        {insufficient ? (
          <Text className="ck-insufficient">
            可用积分 {balanceText}，积分不足
          </Text>
        ) : null}
        <View className="ck-submit">
          <t-button
            theme="primary"
            size="large"
            block
            shape="round"
            disabled={submitting || addresses.length === 0 || insufficient}
            loading={submitting}
            onTap={() => submitCurrent()}
          >
            确认兑换
          </t-button>
        </View>
      </View>

      {balanceText !== "" ? (
        <View className="ck-balance">
          <Text className="ck-balance__label">积分余额</Text>
          <Text className="ck-balance__value">{balanceText}</Text>
        </View>
      ) : null}
    </View>
  );
}

export default CheckoutPage;
