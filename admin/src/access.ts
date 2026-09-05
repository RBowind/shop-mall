/**
 * Permission helpers.
 *
 * The backend is the only security boundary; these helpers only drive menu and
 * action visibility. A permission that is missing from the session simply
 * hides the corresponding UI, never the API route. The permission codes mirror
 * `backend/migrations/0002_seed.up.sql` plus `0003_add_admin_read_permissions.up.sql`
 * and the `x-required-permission` contract in `docs/api/openapi.yaml`.
 */

export const PERMISSIONS = {
  productRead: "product:read",
  productWrite: "product:write",
  imageWrite: "image:write",
  orderRead: "order:read",
  orderShip: "order:ship",
  refundRead: "refund:read",
  refundApprove: "refund:approve",
  pointsAdjust: "points:adjust",
  userRead: "user:read",
  auditRead: "audit:read",
  roleManage: "role:manage",
  adminSelf: "admin:self",
} as const;

export type PermissionCode = (typeof PERMISSIONS)[keyof typeof PERMISSIONS];

export const PERMISSION_CODES: readonly string[] = Object.values(PERMISSIONS);

/**
 * Umi plugin-access factory (default export contract of `src/access.ts`).
 *
 * Maps the session returned by `getInitialState` to a plain `{ code: boolean }`
 * record, one flag per permission code. Route configs carry `access:
 * "product:read"` style names that resolve against these keys, so access
 * control and the backend permission codes cannot drift apart. The backend is
 * the only security boundary; this map only decides menu rendering and
 * direct-URL guards.
 */
type AccessInitialState = {
  session?: { permissions?: readonly string[] } | null;
} | null | undefined;

export default function accessFactory(
  initialState: AccessInitialState,
): Record<string, boolean> {
  const session = initialState?.session ?? null;
  const permissions = session?.permissions;
  const result: Record<string, boolean> = { authenticated: session !== null };
  for (const code of PERMISSION_CODES) {
    result[code] = hasPermission(permissions, code);
  }
  return result;
}

export function hasPermission(
  permissions: readonly string[] | undefined | null,
  code: string,
): boolean {
  if (!permissions) return false;
  return permissions.includes(code);
}

/** Returns true when the permission set contains at least one of the codes. */
export function canAny(
  permissions: readonly string[] | undefined | null,
  codes: readonly string[],
): boolean {
  return codes.some((code) => hasPermission(permissions, code));
}

/** Returns true when the permission set contains every code. */
export function canAll(
  permissions: readonly string[] | undefined | null,
  codes: readonly string[],
): boolean {
  return codes.every((code) => hasPermission(permissions, code));
}

export interface ProductActions {
  canList: boolean;
  canWrite: boolean;
  canUpload: boolean;
}

/**
 * Product page actions. The upload button is part of the create/edit form, so
 * it requires both the product write context and the backend image:write
 * permission that protects POST /api/admin/v1/images.
 */
export function productActions(
  permissions: readonly string[] | undefined | null,
): ProductActions {
  return {
    canList: hasPermission(permissions, PERMISSIONS.productRead),
    canWrite: hasPermission(permissions, PERMISSIONS.productWrite),
    canUpload:
      hasPermission(permissions, PERMISSIONS.productWrite) &&
      hasPermission(permissions, PERMISSIONS.imageWrite),
  };
}

export interface OrderActions {
  canList: boolean;
  canShip: boolean;
}

export function orderActions(
  permissions: readonly string[] | undefined | null,
): OrderActions {
  return {
    canList: hasPermission(permissions, PERMISSIONS.orderRead),
    canShip: hasPermission(permissions, PERMISSIONS.orderShip),
  };
}

export interface RefundActions {
  canList: boolean;
  canApprove: boolean;
}

export function refundActions(
  permissions: readonly string[] | undefined | null,
): RefundActions {
  return {
    canList: hasPermission(permissions, PERMISSIONS.refundRead),
    canApprove: hasPermission(permissions, PERMISSIONS.refundApprove),
  };
}

export function canAdjustPoints(
  permissions: readonly string[] | undefined | null,
): boolean {
  return hasPermission(permissions, PERMISSIONS.pointsAdjust);
}