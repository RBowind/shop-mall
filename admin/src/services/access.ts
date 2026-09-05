/**
 * Administrator access service (roles and administrator accounts).
 *
 * Both endpoints require the role:manage permission. Administrator creation is
 * bootstrap-only in v1, so the frontend only reads: no create/delete UI is
 * provided, and the API offers no create/delete operations anyway. The roles
 * list doubles as the permission source for sessions whose login response did
 * not carry a permission list.
 */

import { getTransport, parsePage } from "./transport.ts";
import type { components } from "./generated/api";
import type { AdminUser, Role } from "./types.ts";

export interface AdminUserPage {
  list: AdminUser[];
  total: number;
  page: number;
  page_size: number;
}

export async function listRoles(): Promise<Role[]> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/admin/v1/roles",
  });
  const envelope = response.data as components["schemas"]["RoleListResponse"];
  return envelope.data.list ?? [];
}

export async function listAdminUsers(
  query: { page?: number; page_size?: number } = {},
): Promise<AdminUserPage> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/admin/v1/admin-users",
    query: { page: query.page, page_size: query.page_size },
  });
  const envelope = response.data as components["schemas"]["AdminUserListResponse"];
  return parsePage<AdminUser>(envelope.data);
}