/**
 * Convenience aliases over the generated OpenAPI types.
 *
 * The generated file is types-only; these aliases exist so services, stores
 * and pages read the contract without touching `generated/api` path strings.
 * All int64 fields remain strings (ids, points, price, address version).
 */

import type { components } from "./generated/api.ts";

export type User = components["schemas"]["User"];
export type Product = components["schemas"]["Product"];
export type ProductStatus = components["schemas"]["ProductStatus"];
export type CategoryInfo = components["schemas"]["CategoryInfo"];
export type CartItem = components["schemas"]["CartItem"];
export type Address = components["schemas"]["Address"];
export type AddressCreateRequest = components["schemas"]["AddressCreateRequest"];
export type AddressUpdateRequest = components["schemas"]["AddressUpdateRequest"];
export type Order = components["schemas"]["Order"];
export type OrderItem = components["schemas"]["OrderItem"];
export type OrderStatus = components["schemas"]["OrderStatus"];

export type BuyerLoginResponse = components["schemas"]["BuyerLoginResponse"];
export type UserResponse = components["schemas"]["UserResponse"];
export type ProductResponse = components["schemas"]["ProductResponse"];
export type ProductListResponse = components["schemas"]["ProductListResponse"];
export type CartResponse = components["schemas"]["CartResponse"];
export type CartItemResponse = components["schemas"]["CartItemResponse"];
export type AddressResponse = components["schemas"]["AddressResponse"];
export type AddressListResponse = components["schemas"]["AddressListResponse"];
export type OrderResponse = components["schemas"]["OrderResponse"];
export type OrderListResponse = components["schemas"]["OrderListResponse"];
export type EmptyResponse = components["schemas"]["EmptyResponse"];
export type ErrorResponse = components["schemas"]["ErrorResponse"];