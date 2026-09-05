/**
 * Taro/React render for the product card (two-column grid cell). Pure
 * display. The add-to-cart button is the last item of the meta row (an
 * in-flow sibling of price/stock, NOT an absolute overlay: the category
 * pane is narrow and the overlay's reserved padding crushed 5-digit
 * prices). Because it sits inside the card tap zone it stops propagation
 * so a cart tap never also fires the card handler. It is a plain View with
 * the globally registered `t-icon` (a native Taro Button's `size="mini"`
 * UA styles have higher specificity than the class and crushed the glyph).
 * All price/stock text comes from the server via `toProductCardVM`.
 */

import { Image, Text, View } from "@tarojs/components";

import { toLocalImageUrl } from "../../lib/image-url";
import type { Product } from "../../services/types";
import { toProductCardVM } from "./index";

export interface ProductCardProps {
  product: Product;
  onTap(product: Product): void;
  onAddCart(product: Product): void;
}

export function ProductCard({ product, onTap, onAddCart }: ProductCardProps) {
  const vm = toProductCardVM(product);
  return (
    <View className="product-card">
      <View className="product-card__main" onClick={() => onTap(product)}>
        <View className="product-card__media">
          <Image
            className={
              vm.isSoldOut
                ? "product-card__image product-card__soldout-image"
                : "product-card__image"
            }
            src={toLocalImageUrl(product.main_image)}
            mode="aspectFill"
          />
          {vm.isSoldOut ? (
            <View className="product-card__soldout">
              <Text>已售罄</Text>
            </View>
          ) : null}
        </View>
        <View className="product-card__info">
          <Text
            className={
              vm.isSoldOut
                ? "product-card__name product-card__name--soldout"
                : "product-card__name"
            }
          >
            {product.name}
          </Text>
          <View className="product-card__meta">
            <View className="product-card__price-group">
              <Text className="product-card__price">{vm.priceValue}</Text>
              <Text className="product-card__price-unit">积分</Text>
            </View>
            {vm.stockText ? (
              <Text
                className={
                  vm.isSoldOut
                    ? "product-card__stock product-card__stock--soldout"
                    : "product-card__stock"
                }
              >
                {vm.stockText}
              </Text>
            ) : null}
            <View
              className={vm.isSoldOut ? "product-card__add product-card__add--disabled" : "product-card__add"}
              hoverClass={vm.isSoldOut ? "none" : "product-card__add--hover"}
              onClick={(e) => {
                e.stopPropagation();
                if (!vm.isSoldOut) onAddCart(product);
              }}
            >
              <t-icon name="add" size="18rpx" color={vm.isSoldOut ? "rgba(0,0,0,0.26)" : "#ffffff"} />
            </View>
          </View>
        </View>
      </View>
    </View>
  );
}
