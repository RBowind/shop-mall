/**
 * Content-section menu table for the admin shell.
 *
 * Single source of truth for the sidebar: path, label and the permission code
 * required to see the entry. `config/routes.ts` derives the Umi route table
 * (adding the `component` and `access` fields) and the ProLayout sidebar / the
 * post-login default destination derive their visibility from this table, so
 * the permission mapping lives in exactly one place. The backend enforces every
 * permission; this table only drives menu rendering and direct-URL guards.
 */

import { hasPermission } from './access.ts';

export interface MenuItem {
  path: string;
  name: string;
  /** Permission code required to see this entry. */
  permission: string;
}

export const MENU_ITEMS: readonly MenuItem[] = [
  { path: '/products', name: '商品', permission: 'product:read' },
  { path: '/orders', name: '订单', permission: 'order:read' },
  { path: '/refunds', name: '退款', permission: 'refund:read' },
  { path: '/points', name: '积分', permission: 'points:adjust' },
  { path: '/users', name: '会员', permission: 'user:read' },
  { path: '/access', name: '权限', permission: 'role:manage' },
  { path: '/audit-logs', name: '审计', permission: 'audit:read' },
];

export function visibleMenuItems(
  permissions: readonly string[] | undefined | null,
): MenuItem[] {
  return MENU_ITEMS.filter((item) => hasPermission(permissions, item.permission));
}

/** First content destination the permission set can see, used after login and for `/`. */
export function defaultRoutePath(
  permissions: readonly string[] | undefined | null,
): string {
  const first = visibleMenuItems(permissions)[0];
  return first?.path ?? '/products';
}