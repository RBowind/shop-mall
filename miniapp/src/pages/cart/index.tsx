/**
 * Cart page (main package, tabBar). Requires login: `useDidShow` gates through
 * `authStore.requireLogin()` and never auto-retries. Items whose product went
 * off sale or out of stock stay visible but are flagged non-purchasable and
 * cannot be submitted to checkout.
 *
 * Visual follows the OpenDesign cart prototype: white line cards on the grey
 * page, fixed white footer with the 全选 check (static — checkout always takes
 * every purchasable line), the 合计 red big-number + 积分 unit and the blue
 * 去结算 button. The insufficient-balance banner from the prototype needs a
 * balance the cart never fetches, so the button-disabled path stays the only
 * guard (unchanged behavior).
 */

import { Text, View } from "@tarojs/components";
import { useDidShow } from "@tarojs/taro";
import { useState, useSyncExternalStore } from "react";

import { CartItemView } from "../../components/cart-item/cart-item";
import { describeApiError, withTraceId } from "../../lib/errors";
import { sumCartItems } from "../../lib/points";
import { getTaro } from "../../lib/taro";
import { showErrorModal, showErrorToast } from "../../lib/ui";
import { authStore } from "../../stores/auth";
import { cartStore, isNonPurchasable, purchasableItems } from "../../stores/cart";

import "./index.css";

export function CartPage() {
  const [errorText, setErrorText] = useState("");
  const items = useSyncExternalStore(
    cartStore.subscribe,
    () => cartStore.getState().items,
  );
  const purchasable = purchasableItems(items);
  const subtotal = sumCartItems(purchasable);
  const nonPurchasableCount = items.filter((item) => isNonPurchasable(item)).length;

  useDidShow(() => {
    if (!authStore.requireLogin()) return;
    cartStore
      .refresh()
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setErrorText(withTraceId(presentation));
        showErrorToast(err);
      });
  });

  const handleCheckout = () => {
    if (nonPurchasableCount > 0) {
      getTaro().showToast({ title: "有商品已下架，请先移除", icon: "none" });
      return;
    }
    if (items.length === 0) {
      getTaro().showToast({ title: "购物车是空的", icon: "none" });
      return;
    }
    getTaro().navigateTo({ url: "/pages/checkout/index" });
  };

  return (
    <View className="page page--with-footer">
      {errorText ? <View className="error">{errorText}</View> : null}
      {items.map((item) => (
        <CartItemView
          key={item.id}
          item={item}
          onChangeQuantity={(target, quantity) => {
            if (quantity < 1) return;
            cartStore
              .update(target.id, quantity)
              .catch((err: unknown) => showErrorToast(err));
          }}
          onRemove={(target) => {
            cartStore
              .remove(target.id)
              .catch((err: unknown) => showErrorModal(err));
          }}
          onTapDetail={(item) => {
            getTaro().navigateTo({
              url: `/pages/product-detail/index?productId=${encodeURIComponent(item.product_id)}`,
            });
          }}
        />
      ))}
      {items.length === 0 && !errorText ? (
        <View className="empty-block">
          <t-empty description="购物车还是空的" />
          <t-button
            theme="primary"
            variant="outline"
            onTap={() => getTaro().switchTab({ url: "/pages/index/index" })}
          >
            去逛逛
          </t-button>
        </View>
      ) : null}
      {items.length > 0 ? (
        <View className="cart-footer">
          <View className="cart-footer__all">
            <View
              className={
                purchasable.length > 0
                  ? "cart-item__check cart-item__check--on"
                  : "cart-item__check"
              }
            >
              {purchasable.length > 0 ? (
                <t-icon name="check" size="26rpx" color="#ffffff" />
              ) : null}
            </View>
            <Text className="cart-footer__all-label">全选</Text>
          </View>
          <View className="cart-footer__total">
            <Text className="cart-footer__total-label">合计</Text>
            <Text className="cart-footer__total-num">{subtotal}</Text>
            <Text className="cart-footer__total-unit">积分</Text>
          </View>
          <t-button
            theme="primary"
            size="large"
            disabled={nonPurchasableCount > 0 || items.length === 0}
            onTap={() => handleCheckout()}
          >
            去结算
          </t-button>
        </View>
      ) : null}
    </View>
  );
}

export default CartPage;
