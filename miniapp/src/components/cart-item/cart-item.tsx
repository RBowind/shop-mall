/**
 * Taro/React render for a cart line. Displays server-owned data, flags
 * non-purchasable items (off sale or out of stock) and emits
 * quantity/remove/detail events; the parent page performs the cart mutations.
 *
 * Visual follows the OpenDesign cart prototype: circular check marker (blue
 * when the line goes to checkout, disabled grey when non-purchasable), square
 * image, two-line name, grey "单价：N 积分" sub-line and a right-hand column
 * with the TDesign stepper above a bordered 删除 chip. The check marker is a
 * static reflection of what the checkout submits (every purchasable line);
 * the store owns no per-item selection, so it carries no tap handler.
 */

import { Image, Text, View } from "@tarojs/components";

import { toLocalImageUrl } from "../../lib/image-url";
import type { CartItem } from "../../services/types";
import { toCartItemVM } from "./index";

export interface CartItemProps {
  item: CartItem;
  onChangeQuantity(item: CartItem, quantity: number): void;
  onRemove(item: CartItem): void;
  onTapDetail(item: CartItem): void;
}

export function CartItemView({
  item,
  onChangeQuantity,
  onRemove,
  onTapDetail,
}: CartItemProps) {
  const vm = toCartItemVM(item);
  return (
    <View
      className={vm.nonPurchasable ? "cart-item cart-item--disabled" : "cart-item"}
    >
      <View
        className={
          vm.nonPurchasable
            ? "cart-item__check cart-item__check--disabled"
            : "cart-item__check cart-item__check--on"
        }
      >
        {vm.nonPurchasable ? null : (
          <t-icon name="check" size="26rpx" color="#ffffff" />
        )}
      </View>
      <Image
        className="cart-item__image"
        src={toLocalImageUrl(item.product.main_image)}
        mode="aspectFill"
        onClick={() => onTapDetail(item)}
      />
      <View className="cart-item__body">
        <Text className="cart-item__name">{vm.productName}</Text>
        <Text className="cart-item__unit">单价：{vm.priceText}</Text>
        {vm.nonPurchasable ? (
          <View className="cart-item__flag">
            <t-tag theme="danger" variant="light" size="small">
              {vm.nonPurchasableReason}
            </t-tag>
          </View>
        ) : null}
      </View>
      <View className="cart-item__side">
        <t-stepper
          value={vm.quantity}
          min={1}
          theme="filled"
          size="small"
          disabled={vm.nonPurchasable}
          onChange={(e: { detail: { value: number } }) =>
            onChangeQuantity(item, Number(e.detail.value))
          }
        />
        <Text className="cart-item__remove" onClick={() => onRemove(item)}>
          删除
        </Text>
      </View>
    </View>
  );
}
