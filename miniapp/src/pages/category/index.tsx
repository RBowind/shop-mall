/**
 * Category page (main package, tabBar). Top: full-width white strip holding
 * the search pill (same affordance as home, navigates to the search
 * subpackage). Below: a white vertical category rail (active item = red text
 * + left red bar on the gray content background) and a two-column product
 * grid, paged by scroll to bottom. Entry may carry ?category=<id> (home
 * quick-nav) which preselects the rail. Data flow is untouched: GET
 * /api/v1/categories feeds the rail, GET /api/v1/products?category= feeds the
 * grid.
 */

import { useDidShow, useRouter } from "@tarojs/taro";
import { ScrollView, Text, View } from "@tarojs/components";
import { useState } from "react";

import { ProductCard } from "../../components/product-card/product-card";
import { describeApiError, withTraceId } from "../../lib/errors";
import { getTaro } from "../../lib/taro";
import { showErrorToast } from "../../lib/ui";
import { listCategories, listProducts } from "../../services/products";
import type { CategoryInfo, Product } from "../../services/types";
import { authStore } from "../../stores/auth";
import { cartStore } from "../../stores/cart";

import "./index.css";

const PAGE_SIZE = 10;

export function CategoryPage() {
  const router = useRouter();
  const [categories, setCategories] = useState<CategoryInfo[]>([]);
  const [activeCategory, setActiveCategory] = useState("");
  const [products, setProducts] = useState<Product[]>([]);
  const [page, setPage] = useState(1);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [errorText, setErrorText] = useState("");

  const loadCategories = () => {
    listCategories()
      .then((list) => setCategories(list))
      .catch((err: unknown) => {
        showErrorToast(err);
      });
  };

  const loadProducts = (category: string, nextPage: number, reset: boolean) => {
    setLoading(true);
    setErrorText("");
    listProducts({ page: nextPage, page_size: PAGE_SIZE, category })
      .then((result) => {
        const merged = reset ? result.list : products.concat(result.list);
        setProducts(merged);
        setPage(result.page);
        setHasMore(merged.length < result.total);
        setLoading(false);
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setLoading(false);
        setErrorText(withTraceId(presentation));
      });
  };

  useDidShow(() => {
    loadCategories();
    // Home's quick-nav links carry ?category=<id>: preselect it on entry.
    // The param is read defensively — without it the page behaves exactly
    // as before ("全部" selected).
    let target = "";
    try {
      const param = String(router.params?.category ?? "");
      if (param) {
        target = param;
      }
    } catch {
      target = "";
    }
    setActiveCategory(target);
    loadProducts(target, 1, true);
  });

  const handleCategoryTap = (category: string) => {
    if (category === activeCategory) return;
    setActiveCategory(category);
    loadProducts(category, 1, true);
  };

  const handleReachBottom = () => {
    if (!hasMore || loading) return;
    loadProducts(activeCategory, page + 1, false);
  };

  const handleSearchTap = () => {
    getTaro().navigateTo({ url: "/pages/search/index" });
  };

  const handleProductTap = (product: Product) => {
    getTaro().navigateTo({
      url: `/pages/product-detail/index?productId=${encodeURIComponent(product.id)}`,
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
    <View className="page category-page">
      <View className="category-search-strip">
        <View className="search-entry" onClick={handleSearchTap}>
          <t-icon name="search" size="32rpx" color="rgba(0, 0, 0, 0.4)" />
          <Text className="search-entry__placeholder">搜索积分商品</Text>
        </View>
      </View>
      <View className="category-body">
        <ScrollView className="category-sidebar" scrollY>
          <View
            className={
              activeCategory === ""
                ? "category-item category-item--active"
                : "category-item"
            }
            onClick={() => handleCategoryTap("")}
          >
            <Text>全部</Text>
          </View>
          {categories.map((category) => (
            <View
              key={category.id}
              className={
                activeCategory === category.id
                  ? "category-item category-item--active"
                  : "category-item"
              }
              onClick={() => handleCategoryTap(category.id)}
            >
              <Text>{category.name}</Text>
            </View>
          ))}
        </ScrollView>
        <ScrollView
          className="category-content"
          scrollY
          onScrollToLower={handleReachBottom}
          lowerThreshold={120}
        >
          {loading && products.length === 0 ? (
            <View className="loading-block">
              <t-loading theme="circular" size="48rpx" text="加载中…" />
            </View>
          ) : null}
          {errorText ? (
            <View className="address-error">
              <Text className="error">{errorText}</Text>
              <t-button
                size="small"
                variant="outline"
                theme="primary"
                onTap={() => loadProducts(activeCategory, 1, true)}
              >
                重试
              </t-button>
            </View>
          ) : null}
          {products.length > 0 ? (
            <Text className="category-count">
              共 {products.length} 件可兑商品
            </Text>
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
              <t-empty description="该分类暂未上架商品" />
            </View>
          ) : null}
          {hasMore && products.length > 0 ? (
            <View className="load-more">
              <Text className="load-more__text">上拉加载更多</Text>
            </View>
          ) : null}
        </ScrollView>
      </View>
    </View>
  );
}

export default CategoryPage;
