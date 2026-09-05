/**
 * 会员管理页 (user:read).
 *
 * ProTable 列：ID / 昵称 / 积分余额 / 注册时间。支持按 ID 或昵称搜索。
 * 数据来源：GET /api/admin/v1/users（已 seed user:read 权限码）。
 *
 * 与"积分调整"配对：积分调整原本要求手填 int64 用户 ID，现在后台没有
 * 列买家 ID 的入口，是死结。本页是它的前置依赖 —— 让运营至少能选一个
 * 买家跳到积分调整，而不是输一个不知道的值。
 */
import { useRef } from 'react';
import { ProTable } from '@ant-design/pro-components';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { history } from '@umijs/max';

import { listMembers } from '@/services/users';
import type { Member } from '@/services/users';

export default function UsersPage(): React.ReactElement {
  const actionRef = useRef<ActionType>();

  const columns: ProColumns<Member>[] = [
    { title: 'ID', dataIndex: 'id', width: 160, ellipsis: true, copyable: true },
    { title: '昵称', dataIndex: 'nickname', ellipsis: true },
    {
      title: '积分余额',
      dataIndex: 'points_balance',
      width: 120,
      render: (_, record) => (record.points_balance === '0' ? '0' : record.points_balance),
    },
    {
      title: '注册时间',
      dataIndex: 'created_at',
      width: 180,
      valueType: 'dateTime',
    },
    {
      title: '操作',
      valueType: 'option',
      width: 160,
      fixed: 'right',
      render: (_, record) => [
        <a
          key="adjust"
          onClick={() => history.push(`/points?user_id=${encodeURIComponent(record.id)}`)}
        >
          调整积分
        </a>,
      ],
    },
  ];

  return (
    <ProTable<Member>
      rowKey="id"
      actionRef={actionRef}
      columns={columns}
      headerTitle="会员列表"
      search={{
        labelWidth: 'auto',
        defaultCollapsed: false,
      }}
      pagination={{
        defaultPageSize: 20,
        pageSizeOptions: [10, 20, 50],
        showSizeChanger: true,
      }}
      request={async (params) => {
        const keyword =
          typeof params.keyword === 'string' && params.keyword.trim() !== ''
            ? params.keyword.trim()
            : undefined;
        const page = await listMembers({
          page: params.current ?? 1,
          page_size: params.pageSize ?? 20,
          keyword,
        });
        return { data: page.list, total: page.total, success: true };
      }}
    />
  );
}