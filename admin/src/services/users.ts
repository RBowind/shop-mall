/**
 * Member (buyer) listing service. Drives the admin 会员管理 page
 * (`src/pages/users`). The endpoint lives at /api/admin/v1/users and is
 * gated by the user:read permission (seeded in 0003_add_admin_read_permissions).
 *
 * Backend projection (see backend/internal/admin/handler.go: userResponse):
 *   id (int64 as string), nickname, points_balance (string), created_at
 */
import { getTransport, parsePage } from "./transport.ts";

export interface Member {
  id: string;
  nickname: string;
  points_balance: string;
  created_at: string;
}

export interface MemberPage {
  list: Member[];
  total: number;
  page: number;
  page_size: number;
}

export async function listMembers(
  query: { page?: number; page_size?: number; keyword?: string } = {},
): Promise<MemberPage> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/admin/v1/users",
    query: {
      page: query.page,
      page_size: query.page_size,
      keyword: query.keyword,
    },
  });
  const envelope = response.data as { data?: { list?: Member[]; total?: number; page?: number; page_size?: number } };
  return parsePage<Member>(envelope.data ?? {});
}