/**
 * Cart item. Renders a server-owned cart line, flags non-purchasable items
 * (product off sale or out of stock) and emits quantity/remove events. The
 * host page performs the cart mutations through the cart store.
 */

import { mulIntString } from "../../lib/points.ts";
import type { MiniComponentDefinition } from "../../lib/taro.ts";
import type { CartItem } from "../../services/types.ts";
import { isNonPurchasable } from "../../stores/cart.ts";

export interface CartItemVM {
  productName: string;
  priceText: string;
  lineTotalText: string;
  quantity: number;
  nonPurchasable: boolean;
  nonPurchasableReason: string;
}

export function toCartItemVM(item: CartItem): CartItemVM {
  const lineTotal = mulIntString(item.product.price_points, item.quantity);
  return {
    productName: item.product.name,
    priceText: `${item.product.price_points} 积分`,
    lineTotalText: `${lineTotal} 积分`,
    quantity: item.quantity,
    nonPurchasable: isNonPurchasable(item),
    nonPurchasableReason: item.product.status === "off_sale" ? "商品已下架" : "库存不足",
  };
}

export interface CartItemHandlers {
  onChangeQuantity(item: CartItem, quantity: number): void;
  onRemove(item: CartItem): void;
  onTapDetail(item: CartItem): void;
}

export function createCartItemComponent(
  handlers: CartItemHandlers,
): MiniComponentDefinition {
  return {
    name: "cart-item",
    properties: { item: { type: Object } },
    data: {},
    methods: {
      onChangeQuantity(this: unknown, quantity: unknown) {
        const item = (this as { item?: CartItem }).item;
        if (item && typeof quantity === "number") {
          handlers.onChangeQuantity(item, quantity);
        }
      },
      onRemove(this: unknown) {
        const item = (this as { item?: CartItem }).item;
        if (item) handlers.onRemove(item);
      },
      onTapDetail(this: unknown) {
        const item = (this as { item?: CartItem }).item;
        if (item) handlers.onTapDetail(item);
      },
    },
  };
}