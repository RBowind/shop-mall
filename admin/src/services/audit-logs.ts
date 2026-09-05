/**
 * Audit log listing service. Drives the admin 审计日志 page
 * (`src/pages/audit-logs`). The endpoint lives at /api/admin/v1/audit-logs
 * and is gated by the audit:read permission (seeded in 0003).
 *
 * Backend projection (see backend/internal/admin/handler.go: auditLogResponse):
 *   id, actor_admin_id?, actor_role, action, target_type, target_id?,
 *   result, before_data, after_data, trace_id, created_at
 */
import { getTransport, parsePage } from "./transport.ts";

export interface AuditLog {
  id: string;
  actor_admin_id?: string;
  actor_role: string;
  action: string;
  target_type: string;
  target_id?: string;
  result: string;
  before_data: Record<string, unknown>;
  after_data: Record<string, unknown>;
  trace_id: string;
  created_at: string;
}

export interface AuditLogPage {
  list: AuditLog[];
  total: number;
  page: number;
  page_size: number;
}

export async function listAuditLogs(
  query: { page?: number; page_size?: number } = {},
): Promise<AuditLogPage> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/admin/v1/audit-logs",
    query: { page: query.page, page_size: query.page_size },
  });
  const envelope = response.data as { data?: { list?: AuditLog[]; total?: number; page?: number; page_size?: number } };
  return parsePage<AuditLog>(envelope.data ?? {});
}