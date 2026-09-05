/**
 * 审计日志查询页 (audit:read).
 *
 * ProTable 列：操作人 / 动作 / 对象 / 结果 / 时间。结果成功/失败用状态色
 * 区分。商品/订单/退款/积分/权限/会员等写操作都写 audit_logs，这里都能查到。
 */
import { useRef } from 'react';
import { ProTable } from '@ant-design/pro-components';
import type { ActionType, ProColumns } from '@ant-design/pro-components';

import { listAuditLogs } from '@/services/audit-logs';
import type { AuditLog } from '@/services/audit-logs';

const RESULT_VALUE_ENUM = {
  success: { text: '成功', status: 'Success' },
  failure: { text: '失败', status: 'Error' },
} as const;

function actorLabel(record: AuditLog): string {
  return record.actor_admin_id ? `管理员 #${record.actor_admin_id}` : '系统';
}

export default function AuditLogsPage(): React.ReactElement {
  const actionRef = useRef<ActionType>();

  const columns: ProColumns<AuditLog>[] = [
    {
      title: '操作人',
      width: 160,
      render: (_, record) => actorLabel(record),
    },
    {
      title: '动作',
      dataIndex: 'action',
      width: 200,
      ellipsis: true,
      render: (_, record) => `${record.action}`,
    },
    {
      title: '对象',
      width: 160,
      render: (_, record) =>
        record.target_id
          ? `${record.target_type} #${record.target_id}`
          : record.target_type,
    },
    {
      title: '结果',
      dataIndex: 'result',
      width: 90,
      valueEnum: RESULT_VALUE_ENUM,
    },
    {
      title: 'trace_id',
      dataIndex: 'trace_id',
      width: 220,
      ellipsis: true,
      copyable: true,
    },
    {
      title: '时间',
      dataIndex: 'created_at',
      width: 180,
      valueType: 'dateTime',
    },
  ];

  return (
    <ProTable<AuditLog>
      rowKey="id"
      actionRef={actionRef}
      columns={columns}
      headerTitle="审计日志"
      search={false}
      pagination={{
        defaultPageSize: 20,
        pageSizeOptions: [10, 20, 50],
        showSizeChanger: true,
      }}
      request={async (params) => {
        const page = await listAuditLogs({
          page: params.current ?? 1,
          page_size: params.pageSize ?? 20,
        });
        return { data: page.list, total: page.total, success: true };
      }}
    />
  );
}