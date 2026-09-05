/**
 * Product detail page (subpackage). Public browsing; the detail endpoint
 * serves on-sale products only and returns 404 for off-sale or deleted ones,
 * which is presented as "内容不存在或已下架" with the trace ID.
 *
 * Visual layer follows OpenDesign product-detail.html: full-bleed 1:1 gallery,
 * white info card (red points price + 积分 unit, stock row, name, description),
 * #fff3e8 notice strip, parameter cell card, image-rich detail section and a
 * fixed white action bar (outline primary-color secondary "加入购物车" + solid
 * blue "立即兑换"). Data flow is unchanged from the previous version: the cart
 * button still calls cartStore.add behind authStore.requireLogin(); the
 * exchange button reuses that exact existing call and then opens the existing
 * checkout route (the one the cart footer already uses), so no new API surface
 * is introduced and the TDesign default brand (#0052D9) matches the spec.
 */

import { Image, Swiper, SwiperItem, Text, View } from "@tarojs/components";
import { useLoad } from "@tarojs/taro";
import { useEffect, useState } from "react";

import "./index.css";

import { describeApiError, withTraceId } from "../../lib/errors";
import { toLocalImageUrl } from "../../lib/image-url";
import { getTaro } from "../../lib/taro";
import { showErrorToast } from "../../lib/ui";
import { getProduct } from "../../services/products";
import type { Product } from "../../services/types";
import { authStore } from "../../stores/auth";
import { cartStore } from "../../stores/cart";

/** Mirrors the backend category catalog labels (product.Category). */
const CATEGORY_LABELS: Record<string, string> = {
  digital: "数码",
  home: "家居",
  beauty: "美妆",
  food: "食品",
  apparel: "服饰",
};

export function ProductDetailPage() {
  const [productId, setProductId] = useState("");
  const [product, setProduct] = useState<Product | null>(null);
  const [loading, setLoading] = useState(false);
  const [errorText, setErrorText] = useState("");
  const [addSubmitting, setAddSubmitting] = useState(false);
  const [exchangeSubmitting, setExchangeSubmitting] = useState(false);

  useLoad<{ productId?: string }>((options) => {
    setProductId(options.productId ?? "");
  });

  useEffect(() => {
    if (!productId) return;
    setLoading(true);
    setErrorText("");
    getProduct(productId)
      .then((detail) => {
        setProduct(detail);
        setLoading(false);
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setLoading(false);
        setErrorText(withTraceId(presentation));
      });
  }, [productId]);

  const handleAddCart = () => {
    if (!product) return;
    if (!authStore.requireLogin()) return;
    setAddSubmitting(true);
    cartStore
      .add(product.id, 1)
      .then(() => {
        setAddSubmitting(false);
        getTaro().showToast({ title: "已加入购物车", icon: "success" });
      })
      .catch((err: unknown) => {
        setAddSubmitting(false);
        showErrorToast(err);
      });
  };

  // 立即兑换: the same login gate and the same cartStore.add request the
  // secondary button makes, then navigate to the checkout page the cart
  // footer already opens. Checkout submits every purchasable cart line —
  // existing checkout-page semantics, untouched here.
  const handleExchange = () => {
    if (!product) return;
    if (!authStore.requireLogin()) return;
    setExchangeSubmitting(true);
    cartStore
      .add(product.id, 1)
      .then(() => {
        setExchangeSubmitting(false);
        getTaro().navigateTo({ url: "/pages/checkout/index" });
      })
      .catch((err: unknown) => {
        setExchangeSubmitting(false);
        showErrorToast(err);
      });
  };

  // Gallery prefers the images array; main_image alone (older payloads or
  // snapshots) still renders a single image. URLs are re-based onto the API
  // origin in local acceptance runs (see lib/image-url.ts).
  const gallery = (
    product === null
      ? []
      : product.images && product.images.length > 0
        ? product.images
        : product.main_image
          ? [product.main_image]
          : []
  ).map(toLocalImageUrl);

  const soldOut = product !== null && product.stock <= 0;
  // Read-only view of the session user already held by authStore; never
  // triggers a request. Hidden when the buyer is not logged in.
  const balance = authStore.user?.points_balance ?? "";

  return (
    <View className="page pd-page">
      {errorText ? <View className="pd-error">{errorText}</View> : null}
      {loading && !product ? (
        <View className="loading-block">
          <t-loading theme="circular" size="48rpx" text="加载中…" />
        </View>
      ) : null}
      {product ? (
        <>
          {gallery.length > 0 ? (
            <Swiper
              className="pd-gallery"
              indicatorDots={gallery.length > 1}
              indicatorColor="rgba(0,0,0,0.22)"
              indicatorActiveColor="#e64340"
              circular={gallery.length > 1}
              autoplay={false}
            >
              {gallery.map((src) => (
                <SwiperItem key={src}>
                  <Image className="pd-gallery__image" src={src} mode="aspectFill" />
                </SwiperItem>
              ))}
            </Swiper>
          ) : (
            <View className="pd-gallery pd-gallery--empty">
              <t-empty description="暂无图片" />
            </View>
          )}

          <View className="pd-info">
            <View className="pd-info__price-row">
              <Text className="pd-info__price">{product.price_points}</Text>
              <Text className="pd-info__price-unit">积分</Text>
              <Text className="pd-info__pill">包邮</Text>
            </View>
            <Text className="pd-info__name">{product.name}</Text>
            {product.description ? (
              <Text className="pd-info__desc">{product.description}</Text>
            ) : null}
            <View className="pd-info__meta-row">
              <Text className="pd-info__stock">
                {soldOut ? "暂时缺货 · 补货中" : `库存 ${product.stock} 件`}
              </Text>
              {balance !== "" ? (
                <Text className="pd-info__balance">我的积分 {balance}</Text>
              ) : null}
            </View>
          </View>

          <View className="pd-notice">
            <t-icon name="info-circle-filled" size="30rpx" color="#b26a00" />
            <Text className="pd-notice__text">积分兑换包邮，兑换后不可变更。</Text>
          </View>

          <View className="pd-cells">
            <View className="pd-cell">
              <Text className="pd-cell__k">商品分类</Text>
              <Text className="pd-cell__v">
                {CATEGORY_LABELS[product.category] ?? "未分类"}
              </Text>
            </View>
            <View className="pd-cell">
              <Text className="pd-cell__k">商品编号</Text>
              <Text className="pd-cell__v">No. {product.id}</Text>
            </View>
            <View className="pd-cell">
              <Text className="pd-cell__k">库存状态</Text>
              <Text className="pd-cell__v">
                {soldOut ? "已售罄" : `现货 ${product.stock} 件`}
              </Text>
            </View>
            <View className="pd-cell">
              <Text className="pd-cell__k">兑换方式</Text>
              <Text className="pd-cell__v">积分全额抵扣 · 无需支付</Text>
            </View>
            <View className="pd-cell">
              <Text className="pd-cell__k">发货</Text>
              <Text className="pd-cell__v">下单后 48 小时内发出</Text>
            </View>
            <View className="pd-cell">
              <Text className="pd-cell__k">售后</Text>
              <Text className="pd-cell__v">签收 7 天内可申请退积分</Text>
            </View>
          </View>

          {gallery.length > 0 ? (
            <>
              <View className="pd-section-title">
                <Text>图文详情</Text>
              </View>
              <View className="pd-rich">
                {gallery.map((src) => (
                  <Image
                    key={src}
                    className="pd-rich__image"
                    src={src}
                    mode="widthFix"
                  />
                ))}
              </View>
            </>
          ) : null}

          <View className="pd-bar">
            <t-button
              className="pd-bar__btn"
              theme="primary"
              variant="outline"
              size="large"
              shape="round"
              disabled={soldOut}
              loading={addSubmitting}
              onTap={() => handleAddCart()}
            >
              加入购物车
            </t-button>
            <t-button
              className="pd-bar__btn pd-bar__btn--main"
              theme="primary"
              size="large"
              shape="round"
              disabled={soldOut}
              loading={exchangeSubmitting}
              onTap={() => handleExchange()}
            >
              {soldOut ? "已售罄" : "立即兑换"}
            </t-button>
          </View>
        </>
      ) : null}
    </View>
  );
}

export default ProductDetailPage;
