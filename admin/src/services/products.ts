/**
 * Administrator product service.
 *
 * All writes (`createProduct`, `updateProduct`, `uploadImage`) go through the
 * generated transport, which injects the CSRF header from the csrf_token
 * cookie. Product images are stored as object keys: a product references the
 * `key` returned by `uploadImage`, while the read API returns public URLs in
 * `main_image`.
 */

import { getTransport, parsePage, unwrap } from "./transport.ts";
import type { components } from "./generated/api";
import type { Product, ProductStatus, ProductWriteRequest } from "./types.ts";

export interface ProductListQuery {
  page?: number;
  page_size?: number;
  status?: ProductStatus;
}

export interface ProductPage {
  list: Product[];
  total: number;
  page: number;
  page_size: number;
}

export async function listProducts(
  query: ProductListQuery = {},
): Promise<ProductPage> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/admin/v1/products",
    query: { page: query.page, page_size: query.page_size, status: query.status },
  });
  const envelope = response.data as components["schemas"]["ProductListResponse"];
  return parsePage<Product>(envelope.data);
}

export async function getProduct(productId: string): Promise<Product> {
  const response = await getTransport()({
    method: "GET",
    path: `/api/admin/v1/products/${encodeURIComponent(productId)}`,
  });
  return unwrap<Product>(response);
}

export async function createProduct(input: ProductWriteRequest): Promise<Product> {
  const response = await getTransport()({
    method: "POST",
    path: "/api/admin/v1/products",
    body: input,
  });
  return unwrap<Product>(response);
}

export async function updateProduct(
  productId: string,
  input: ProductWriteRequest,
): Promise<Product> {
  const response = await getTransport()({
    method: "PATCH",
    path: `/api/admin/v1/products/${encodeURIComponent(productId)}`,
    body: input,
  });
  return unwrap<Product>(response);
}

/**
 * Uploads a product image as multipart/form-data. The transport detects the
 * FormData body and does not JSON-encode it; the browser sets the boundary.
 * Returns the server-generated object key plus a public URL.
 */
export async function uploadImage(
  file: File,
): Promise<components["schemas"]["ImageResponse"]> {
  const form = new FormData();
  form.append("file", file);
  const response = await getTransport()({
    method: "POST",
    path: "/api/admin/v1/images",
    body: form,
  });
  return unwrap<components["schemas"]["ImageResponse"]>(response);
}