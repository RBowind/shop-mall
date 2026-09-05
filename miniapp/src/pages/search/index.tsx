/**
 * Search page (subpackage). The top input auto-focuses; submit runs the same
 * GET /api/v1/products?keyword= request via the keyboard search key or the
 * blue 搜索 text button on the right of the bar. Empty input shows a plain
 * hint line; results render in the shared two-column grid, misses fall back
 * to t-empty. The keyword hits a case-insensitive substring match against
 * product names server-side.
 */

import { Input, Text, View } from "@tarojs/components";
import { useState } from "react";

import { ProductCard } from "../../components/product-card/product-card";
import { describeApiError, withTraceId } from "../../lib/errors";
import { getTaro } from "../../lib/taro";
import { showErrorToast } from "../../lib/ui";
import { listProducts } from "../../services/products";
import type { Product } from "../../services/types";
import { authStore } from "../../stores/auth";
import { cartStore } from "../../stores/cart";

import "./index.css";

export function SearchPage() {
  const [keyword, setKeyword] = useState("");
  const [submitted, setSubmitted] = useState("");
  const [products, setProducts] = useState<Product[]>([]);
  const [loading, setLoading] = useState(false);
  const [errorText, setErrorText] = useState("");

  const runSearch = (value: string) => {
    const term = value.trim();
    if (!term) {
      setSubmitted("");
      setProducts([]);
      return;
    }
    setSubmitted(term);
    setLoading(true);
    setErrorText("");
    listProducts({ page: 1, page_size: 50, keyword: term })
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

  const handleConfirm = (event: { detail: { value: string } }) => {
    runSearch(event.detail.value);
  };

  const handleSubmitTap = () => {
    runSearch(keyword);
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
    <View className="page search-page">
      <View className="search-bar">
        <t-icon name="search" size="32rpx" color="rgba(0, 0, 0, 0.4)" />
        <Input
          className="search-bar__input"
          type="text"
          placeholder="搜索耳机 / 键盘 / 台灯…"
          confirmType="search"
          focus
          value={keyword}
          onInput={(event) => setKeyword(event.detail.value)}
          onConfirm={handleConfirm}
        />
        <Text className="search-bar__submit" onClick={handleSubmitTap}>
          搜索
        </Text>
      </View>
      {errorText ? (
        <View className="address-error">
          <Text className="error">{errorText}</Text>
          <t-button
            size="small"
            variant="outline"
            theme="primary"
            onTap={() => runSearch(submitted)}
          >
            重试
          </t-button>
        </View>
      ) : null}
      {loading ? (
        <View className="loading-block">
          <t-loading theme="circular" size="48rpx" text="搜索中…" />
        </View>
      ) : null}
      {!loading && submitted && products.length === 0 && !errorText ? (
        <View className="empty-block">
          <t-empty description={`未找到与「${submitted}」相关的商品`} />
        </View>
      ) : null}
      {!loading && submitted && products.length > 0 ? (
        <View className="search-result">
          <Text className="search-result__summary">
            找到 {products.length} 件「{submitted}」相关商品
          </Text>
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
        </View>
      ) : null}
      {!submitted && !loading ? (
        <View className="search-idle">
          <Text className="search-idle__hint">输入关键词搜索商品</Text>
        </View>
      ) : null}
    </View>
  );
}

export default SearchPage;
