/**
 * Product list page (main package, tabBar). Public: browsing does not require
 * login. Add-to-cart is gated: unauthenticated users are guided to the profile
 * tab; the gate never auto-retries.
 *
 * Visual structure follows the OpenDesign home.html screen: capsule search
 * entry → red-gradient points banner → horizontal category quick-nav (deduped
 * client-side from the loaded products, deep-links the category tab) →
 * two-column featured grid. Category keys are the backend catalog ids
 * (see backend/internal/product/category.go); labels/icons are display maps
 * with raw-key fallback.
 */

import { useDidShow, usePullDownRefresh } from "@tarojs/taro";
import { ScrollView, Text, View } from "@tarojs/components";
import { useMemo, useState } from "react";

import { ProductCard } from "../../components/product-card/product-card";
import { describeApiError, withTraceId } from "../../lib/errors";
import { getTaro } from "../../lib/taro";
import { showErrorToast } from "../../lib/ui";
import { listProducts } from "../../services/products";
import type { Product } from "../../services/types";
import { authStore } from "../../stores/auth";
import { cartStore } from "../../stores/cart";

import "./index.css";

/** Display labels per stable category key (fallback: the raw key itself). */
const CATEGORY_LABELS: Record<string, string> = {
  digital: "数码",
  home: "家居",
  beauty: "美妆",
  food: "食品",
  apparel: "服饰",
};

/** t-icon name per stable category key (fallback: gift). */
const CATEGORY_ICONS: Record<string, string> = {
  digital: "mobile",
  home: "home",
  beauty: "palette",
  food: "fork",
  apparel: "shop",
};

export function IndexPage() {
  const [products, setProducts] = useState<Product[]>([]);
  const [loading, setLoading] = useState(false);
  const [errorText, setErrorText] = useState("");

  const loadProducts = () => {
    setLoading(true);
    setErrorText("");
    listProducts()
      .then((result) => {
        setProducts(result.list);
        setLoading(false);
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setLoading(false);
        setErrorText(withTraceId(presentation));
      });
  };

  useDidShow(() => {
    loadProducts();
  });
  usePullDownRefresh(() => {
    loadProducts();
    getTaro().stopPullDownRefresh?.();
  });

  const categories = useMemo(() => {
    const seen: string[] = [];
    for (const p of products) {
      if (p.category && !seen.includes(p.category)) seen.push(p.category);
    }
    return seen;
  }, [products]);

  const handleProductTap = (product: Product) => {
    getTaro().navigateTo({
      url: `/pages/product-detail/index?productId=${encodeURIComponent(product.id)}`,
    });
  };

  const handleSearchTap = () => {
    getTaro().navigateTo({ url: "/pages/search/index" });
  };

  const handleCategoryNav = (category: string) => {
    // switchTab drops query params in the WeChat runtime; the key travels in
    // the url so the category page can pick it up once it reads router params.
    getTaro().switchTab({
      url: `/pages/category/index?category=${encodeURIComponent(category)}`,
    });
  };

  const handleAddCart = (product: Product) => {
    if (!authStore.requireLogin()) return;
    cartStore
      .add(product.id, 1)
      .then(() => {
        getTaro().showToast({ title: "已加入购物车", icon: "success" });
      })
      .catch((err: unknown) => {
        showErrorToast(err);
      });
  };

  return (
    <View className="page home-page">
      <View className="search-entry" onClick={handleSearchTap}>
        <t-icon name="search" size="32rpx" color="rgba(0, 0, 0, 0.4)" />
        <Text className="search-entry__placeholder">搜索积分商品</Text>
      </View>

      <View className="home-banner">
        <View className="home-banner__text">
          <Text className="home-banner__title">积分兑好物</Text>
          <Text className="home-banner__sub">
            全场商品支持积分兑换，兑完即止
          </Text>
        </View>
        <View
          className="home-banner__btn"
          onClick={() => handleCategoryNav("")}
        >
          <Text>全部兑换</Text>
        </View>
      </View>

      {categories.length > 0 ? (
        <ScrollView className="home-cats" scrollX enhanced showScrollbar={false}>
          <View className="home-cats__inner">
            {categories.map((category) => (
              <View
                key={category}
                className="home-cats__item"
                onClick={() => handleCategoryNav(category)}
              >
                <View className="home-cats__icon">
                  <t-icon
                    name={CATEGORY_ICONS[category] ?? "gift"}
                    size="44rpx"
                    color="#0052d9"
                  />
                </View>
                <Text className="home-cats__label">
                  {CATEGORY_LABELS[category] ?? category}
                </Text>
              </View>
            ))}
          </View>
        </ScrollView>
      ) : null}

      {errorText ? (
        <View className="error-block">
          <Text className="text-error">{errorText}</Text>
          <t-button
            size="small"
            variant="outline"
            theme="primary"
            onTap={() => loadProducts()}
          >
            重试
          </t-button>
        </View>
      ) : null}

      <View className="home-section">
        <Text className="home-section__title">为你推荐</Text>
        <Text className="home-section__note">积分兑换 · 包邮到家</Text>
      </View>

      {loading && products.length === 0 ? (
        <View className="loading-block">
          <t-loading theme="circular" size="48rpx" text="加载中…" />
        </View>
      ) : null}
      <View className="product-grid">
        {products.map((product) => (
          <ProductCard
            key={product.id}
            product={product}
            onTap={handleProductTap}
            onAddCart={handleAddCart}
          />
        ))}
      </View>
      {!loading && products.length === 0 && !errorText ? (
        <View className="empty-block">
          <t-empty description="暂无在售商品" />
        </View>
      ) : null}
    </View>
  );
}

export default IndexPage;
