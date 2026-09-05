/**
 * Public product service. Product prices, stock and status are server-owned;
 * the client only renders what the API returns.
 */

import { getTransport } from "../lib/transport.ts";
import type {
  CategoryInfo,
  Product,
  ProductListResponse,
  ProductResponse,
} from "./types.ts";

export interface ProductListQuery {
  page?: number;
  page_size?: number;
  /** Stable category key from the catalog; empty means every category. */
  category?: string;
  /** Case-insensitive substring match against the product name. */
  keyword?: string;
}

export interface ProductListResult {
  list: Product[];
  total: number;
  page: number;
  page_size: number;
}

export async function listProducts(
  query: ProductListQuery = {},
): Promise<ProductListResult> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/v1/products",
    query: {
      page: query.page,
      page_size: query.page_size,
      category: query.category,
      keyword: query.keyword,
    },
  });
  const envelope = response.data as ProductListResponse;
  return {
    list: envelope.data.list ?? [],
    total: envelope.data.total ?? 0,
    page: envelope.data.page ?? 1,
    page_size: envelope.data.page_size ?? 20,
  };
}

export async function listCategories(): Promise<CategoryInfo[]> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/v1/categories",
  });
  const envelope = response.data as { data?: CategoryInfo[] };
  return envelope.data ?? [];
}

export async function getProduct(productId: string): Promise<Product> {
  const response = await getTransport()({
    method: "GET",
    path: `/api/v1/products/${encodeURIComponent(productId)}`,
  });
  return (response.data as ProductResponse).data;
}
