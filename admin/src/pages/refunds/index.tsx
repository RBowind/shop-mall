/**
 * 退款审核页 (refund:read)。
 *
 * 列表复用订单形状：GET /api/admin/v1/refunds 返回 refund_requested 与
 * refunded 状态的订单。通过 / 驳回只在会话带 refund:approve 时渲染，且
 * 仅对 refund_requested 订单开放；驳回必须填写原因（后端 422 校验）。
 * 审批的积分退还、库存恢复、流水与审计字段由后端在同一事务内完成。
 */
import { useRef, useState } from 'react';
import { Button, Input, message, Modal, Popconfirm } from 'antd';
import { ProDescriptions, ProTable } from '@ant-design/pro-components';
import type {
  ActionType,
  ProColumns,
  ProDescriptionsItemProps,
} from '@ant-design/pro-components';
import { useAccess } from '@umijs/max';

import { approveRefund, listRefunds, rejectRefund } from '@/services/refunds';
import type { Order } from '@/services/types';
import { describeAdminError, withTraceId } from '@/requestErrorConfig';

const REFUND_STATUS_VALUE_ENUM = {
  refund_requested: { text: '退款申请中', status: 'Error' },
  refunded: { text: '已退款', status: 'Default' },
} as const;

const DETAIL_COLUMNS: ProDescriptionsItemProps<Order>[] = [
  { title: '订单号', dataIndex: 'order_no' },
  { title: '状态', dataIndex: 'status', valueEnum: REFUND_STATUS_VALUE_ENUM },
  { title: '积分总额', dataIndex: 'total_points' },
  { title: '创建时间', dataIndex: 'created_at', valueType: 'dateTime' },
  { title: '收货人', dataIndex: 'receiver' },
  { title: '电话', dataIndex: 'phone' },
  { title: '退款原因', dataIndex: 'refund_reason', span: 2 },
  { title: '拒绝原因', dataIndex: 'refund_reject_reason', span: 2 },
];

export default function RefundsPage(): React.ReactElement {
  const access = useAccess();
  const canApprove = access['refund:approve'] === true;

  const actionRef = useRef<ActionType>();
  const [detail, setDetail] = useState<Order | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [rejectTarget, setRejectTarget] = useState<Order | null>(null);
  const [rejectReason, setRejectReason] = useState('');
  const [busy, setBusy] = useState(false);

  function closeDetail(): void {
    setDetailOpen(false);
    setDetail(null);
  }

  async function handleApprove(order: Order): Promise<void> {
    setBusy(true);
    try {
      await approveRefund(order.id);
      message.success('退款已通过，积分与库存已回退');
      actionRef.current?.reload();
      closeDetail();
    } catch (err) {
      message.error(withTraceId(describeAdminError(err)));
    } finally {
      setBusy(false);
    }
  }

  async function handleReject(): Promise<void> {
    if (!rejectTarget) return;
    const reason = rejectReason.trim();
    if (reason === '') {
      message.warning('请填写驳回原因');
      return;
    }
    setBusy(true);
    try {
      await rejectRefund(rejectTarget.id, reason);
      message.success('退款申请已驳回');
      setRejectTarget(null);
      setRejectReason('');
      actionRef.current?.reload();
      closeDetail();
    } catch (err) {
      message.error(withTraceId(describeAdminError(err)));
    } finally {
      setBusy(false);
    }
  }

  const columns: ProColumns<Order>[] = [
    { title: '订单号', dataIndex: 'order_no', ellipsis: true },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      valueEnum: REFUND_STATUS_VALUE_ENUM,
    },
    { title: '积分', dataIndex: 'total_points', width: 90 },
    { title: '收货人', dataIndex: 'receiver', width: 110, ellipsis: true },
    { title: '退款原因', dataIndex: 'refund_reason', ellipsis: true },
    { title: '创建时间', dataIndex: 'created_at', width: 180, valueType: 'dateTime' },
    {
      title: '操作',
      valueType: 'option',
      width: 200,
      fixed: 'right',
      render: (_, record) => {
        const actions = [
          <a
            key="detail"
            onClick={() => {
              setDetail(record);
              setDetailOpen(true);
            }}
          >
            详情
          </a>,
        ];
        if (canApprove && record.status === 'refund_requested') {
          actions.push(
            <Popconfirm
              key="approve"
              title="确认通过该退款？积分将退回买家账户。"
              onConfirm={() => void handleApprove(record)}
            >
              <a>通过</a>
            </Popconfirm>,
            <a key="reject" onClick={() => setRejectTarget(record)}>
              驳回
            </a>,
          );
        }
        return actions;
      },
    },
  ];

  return (
    <>
      <ProTable<Order>
        rowKey="id"
        actionRef={actionRef}
        columns={columns}
        headerTitle="退款申请"
        search={false}
        pagination={{
          defaultPageSize: 20,
          pageSizeOptions: [10, 20, 50],
          showSizeChanger: true,
        }}
        request={async (params) => {
          const page = await listRefunds({
            page: params.current ?? 1,
            page_size: params.pageSize ?? 20,
          });
          return { data: page.list, total: page.total, success: true };
        }}
      />
      <Modal
        title={detail ? `订单 ${detail.order_no}` : '订单详情'}
        open={detailOpen}
        width={720}
        onCancel={closeDetail}
        footer={null}
      >
        {detail && (
          <ProDescriptions<Order>
            column={2}
            dataSource={detail}
            columns={DETAIL_COLUMNS}
          />
        )}
      </Modal>
      <Modal
        title={rejectTarget ? `驳回退款 ${rejectTarget.order_no}` : '驳回退款'}
        open={rejectTarget !== null}
        confirmLoading={busy}
        onOk={() => void handleReject()}
        onCancel={() => {
          setRejectTarget(null);
          setRejectReason('');
        }}
        okText="驳回"
        okButtonProps={{ danger: true }}
      >
        <Input.TextArea
          value={rejectReason}
          placeholder="请填写驳回原因（必填，将展示给买家）"
          rows={3}
          maxLength={255}
          onChange={(e) => setRejectReason(e.target.value)}
        />
      </Modal>
    </>
  );
}
