/**
 * Product card. Pure display component: it never performs the add-to-cart
 * side effect. Tapping the card opens the detail page; the add-to-cart button
 * only emits an event for the host page to handle (login gate + cart store).
 */

import type { MiniComponentDefinition } from "../../lib/taro.ts";
import type { Product } from "../../services/types.ts";

export interface ProductCardVM {
  priceText: string;
  /** Bare points number (int64-as-string) for the large red figure; the
   *  small 「积分」 unit renders separately per the brand price format. */
  priceValue: string;
  stockText: string;
  isSoldOut: boolean;
}

/**
 * The meta row fits at most a 4-digit price plus the stock text and the
 * in-flow "+" button (the category pane's cards are narrow). At 5+ digits
 * the price wins: the stock line is dropped — the sold-out state still has
 * its overlay on the image, so nothing is lost but "剩 N".
 */
export function toProductCardVM(product: Product): ProductCardVM {
  const stockText =
    product.price_points.length >= 5
      ? ""
      : product.stock > 0
        ? `剩 ${product.stock}`
        : "已售罄";
  return {
    priceText: `${product.price_points} 积分`,
    priceValue: product.price_points,
    stockText,
    isSoldOut: product.stock <= 0,
  };
}

export interface ProductCardHandlers {
  onTap(product: Product): void;
  onAddCart(product: Product): void;
}

export function createProductCardComponent(
  handlers: ProductCardHandlers,
): MiniComponentDefinition {
  return {
    name: "product-card",
    properties: { product: { type: Object } },
    data: {},
    methods: {
      onTap(this: unknown) {
        const product = (this as { product?: Product }).product;
        if (product) handlers.onTap(product);
      },
      onAddCart(this: unknown) {
        const product = (this as { product?: Product }).product;
        if (product) handlers.onAddCart(product);
      },
    },
  };
}