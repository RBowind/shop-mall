/**
 * Convenience aliases over the generated OpenAPI types.
 *
 * The generated file is types-only; these aliases exist so services, pages and
 * tests read the contract without touching `generated/api` path strings. All
 * int64 fields remain strings (ids, price, points, address version).
 */

import type { components } from "./generated/api";

export type Role = components["schemas"]["Role"];
export type AdminUser = components["schemas"]["AdminUser"];
export type Product = components["schemas"]["Product"];
export type ProductStatus = components["schemas"]["ProductStatus"];
export type ProductWriteRequest = components["schemas"]["ProductWriteRequest"];
export type Order = components["schemas"]["Order"];
export type OrderStatus = components["schemas"]["OrderStatus"];
export type OrderItem = components["schemas"]["OrderItem"];
export type LedgerEntry = components["schemas"]["LedgerEntry"];
export type ImageResponse = components["schemas"]["ImageResponse"];
export type PointsAdjustRequest = components["schemas"]["PointsAdjustRequest"];
export type RefundRejectRequest = components["schemas"]["RefundRejectRequest"];
export type PasswordChangeRequest = components["schemas"]["PasswordChangeRequest"];

export type AdminLoginResponse = components["schemas"]["AdminLoginResponse"];
export type ProductResponse = components["schemas"]["ProductResponse"];
export type ProductListResponse = components["schemas"]["ProductListResponse"];
export type OrderResponse = components["schemas"]["OrderResponse"];
export type OrderListResponse = components["schemas"]["OrderListResponse"];
export type LedgerResponse = components["schemas"]["LedgerResponse"];
export type RoleListResponse = components["schemas"]["RoleListResponse"];
export type AdminUserListResponse = components["schemas"]["AdminUserListResponse"];
export type EmptyResponse = components["schemas"]["EmptyResponse"];