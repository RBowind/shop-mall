/**
 * Administrator authentication service.
 *
 * Login establishes the HttpOnly session cookie plus the readable csrf_token
 * cookie; the transport injects the CSRF header on every subsequent write.
 * Session normalization is tolerant of both the contract shape (login returns
 * `data.role` as a `{ name, permissions }` object) and the current backend
 * shape (login returns `data.role` as a plain role-name string), so the admin
 * console works against either. When the role object carries no permissions
 * the caller can widen the session with `services/access.ts` listRoles().
 */

import { getTransport } from "./transport.ts";
import type { components } from "./generated/api";

export interface AdminSession {
  adminId?: string;
  username: string;
  roleName: string;
  permissions: string[];
  enabled: boolean;
}

type RoleLike = components["schemas"]["Role"];

interface RawSessionData {
  admin_id?: unknown;
  username?: unknown;
  role?: unknown;
  token_version?: unknown;
  enabled?: unknown;
}

export function normalizeSession(data: unknown): AdminSession {
  const raw = (data ?? {}) as RawSessionData;
  const role = raw.role as string | RoleLike | undefined;
  const roleName =
    typeof role === "string" ? role : typeof role?.name === "string" ? role.name : "";
  const permissions =
    typeof role === "string"
      ? []
      : Array.isArray(role?.permissions)
        ? role.permissions.filter((code): code is string => typeof code === "string")
        : [];
  return {
    adminId: typeof raw.admin_id === "string" ? raw.admin_id : undefined,
    username: typeof raw.username === "string" ? raw.username : "",
    roleName,
    permissions,
    enabled: raw.enabled !== false,
  };
}

export async function login(
  username: string,
  password: string,
): Promise<AdminSession> {
  const response = await getTransport()({
    method: "POST",
    path: "/api/admin/v1/auth/login",
    body: { username, password },
  });
  const envelope = response.data as components["schemas"]["AdminLoginResponse"];
  return normalizeSession(envelope.data);
}

export async function logout(): Promise<void> {
  await getTransport()({ method: "POST", path: "/api/admin/v1/auth/logout" });
}

/**
 * Changes the current administrator password. On success the server bumps
 * token_version and invalidates the current JWT, so the caller must send the
 * user back to the login page.
 */
export async function changePassword(
  currentPassword: string,
  newPassword: string,
): Promise<void> {
  await getTransport()({
    method: "POST",
    path: "/api/admin/v1/auth/password",
    body: { current_password: currentPassword, new_password: newPassword },
  });
}

/**
 * Reads the current administrator profile. The backend registers
 * GET /api/admin/v1/auth/me; the OpenAPI contract does not yet list it, so the
 * response is normalized from its envelope without a generated type.
 */
export async function me(): Promise<AdminSession> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/admin/v1/auth/me",
  });
  const envelope = response.data as { data?: unknown };
  return normalizeSession(envelope.data);
}

/**
 * Widens a session that has no permission list with the role's permissions
 * from GET /api/admin/v1/roles. The roles endpoint requires the role:manage
 * permission, so for operators this is a no-op that must not fail the session.
 */
export async function withPermissions(
  session: AdminSession,
  fetchRoles: () => Promise<components["schemas"]["Role"][]>,
): Promise<AdminSession> {
  if (session.permissions.length > 0 || session.roleName === "") {
    return session;
  }
  try {
    const roles = await fetchRoles();
    const matched = roles.find((role) => role.name === session.roleName);
    if (!matched) return session;
    return { ...session, permissions: matched.permissions.slice() };
  } catch {
    return session;
  }
}