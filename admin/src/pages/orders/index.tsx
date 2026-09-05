/**
 * Orders page.
 *
 * ProTable list (pagination + five-state filter) with a ProDescriptions detail
 * drawer-style modal. The ship action renders only for a `paid` order and only
 * when the session carries `order:ship`; the backend still enforces the
 * transition. Items are rendered beneath the descriptions as an antd table.
 */

import { useRef, useState } from 'react';
import { Button, message, Modal, Popconfirm, Table } from 'antd';
import { ProDescriptions, ProTable } from '@ant-design/pro-components';
import type {
  ActionType,
  ProColumns,
  ProDescriptionsItemProps,
} from '@ant-design/pro-components';
import { useAccess } from '@umijs/max';

import { getOrder, listOrders, shipOrder } from '@/services/orders';
import type { Order, OrderItem, OrderStatus } from '@/services/types';
import { describeAdminError, withTraceId } from '@/requestErrorConfig';

const STATUS_VALUE_ENUM = {
  paid: { text: '待发货', status: 'Warning' },
  shipped: { text: '已发货', status: 'Processing' },
  completed: { text: '已完成', status: 'Success' },
  refund_requested: { text: '退款申请中', status: 'Error' },
  refunded: { text: '已退款', status: 'Default' },
} as const;

const DETAIL_COLUMNS: ProDescriptionsItemProps<Order>[] = [
  { title: '状态', dataIndex: 'status', valueEnum: STATUS_VALUE_ENUM },
  { title: '订单号', dataIndex: 'order_no' },
  { title: '积分总额', dataIndex: 'total_points' },
  { title: '收货人', dataIndex: 'receiver' },
  { title: '电话', dataIndex: 'phone' },
  { title: '地址', dataIndex: 'address', span: 2 },
  { title: '创建时间', dataIndex: 'created_at', valueType: 'dateTime' },
  { title: '支付时间', dataIndex: 'paid_at', valueType: 'dateTime' },
  { title: '退款原因', dataIndex: 'refund_reason' },
  { title: '拒绝原因', dataIndex: 'refund_reject_reason' },
];

function itemTotal(order: Order): number {
  return order.items.reduce((sum, item) => sum + item.quantity, 0);
}

function subtotal(item: OrderItem): string {
  return String(Number(item.price_snapshot) * item.quantity);
}

export default function OrdersPage(): React.ReactElement {
  const access = useAccess();
  const canShip = access['order:ship'] === true;

  const actionRef = useRef<ActionType>();
  const [detail, setDetail] = useState<Order | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [shipLoading, setShipLoading] = useState(false);

  function openDetail(orderId: string): void {
    void getOrder(orderId)
      .then((order) => {
        setDetail(order);
        setDetailOpen(true);
      })
      .catch((err) => message.error(withTraceId(describeAdminError(err))));
  }

  async function handleShip(): Promise<void> {
    if (!detail) return;
    setShipLoading(true);
    try {
      const next = await shipOrder(detail.id);
      setDetail(next);
      message.success('订单已发货');
      actionRef?.current?.reload();
    } catch (err) {
      message.error(withTraceId(describeAdminError(err)));
    } finally {
      setShipLoading(false);
    }
  }

  const columns: ProColumns<Order>[] = [
    { title: '订单号', dataIndex: 'order_no', ellipsis: true },
    { title: '状态', dataIndex: 'status', width: 110, valueEnum: STATUS_VALUE_ENUM },
    { title: '积分', dataIndex: 'total_points', width: 90 },
    { title: '收货人', dataIndex: 'receiver', width: 110, ellipsis: true },
    { title: '电话', dataIndex: 'phone', width: 130 },
    { title: '商品数', width: 80, render: (_, record) => itemTotal(record) },
    {
      title: '操作',
      valueType: 'option',
      width: 90,
      fixed: 'right',
      render: (_, record) => [
        <a key="detail" onClick={() => openDetail(record.id)}>
          详情
        </a>,
      ],
    },
  ];

  const canShipThis = detail?.status === 'paid' && canShip;

  return (
    <>
      <ProTable<Order>
        rowKey="id"
        actionRef={actionRef}
        columns={columns}
        search={{ labelWidth: 'auto' }}
        pagination={{
          defaultPageSize: 20,
          pageSizeOptions: [10, 20, 50],
          showSizeChanger: true,
        }}
        request={async (params) => {
          const page = await listOrders({
            page: params.current ?? 1,
            page_size: params.pageSize ?? 20,
            status: params.status ? (params.status as OrderStatus) : undefined,
          });
          return { data: page.list, total: page.total, success: true };
        }}
      />
      <Modal
        title={detail ? `订单 ${detail.order_no}` : '订单详情'}
        open={detailOpen}
        width={720}
        onCancel={() => setDetailOpen(false)}
        footer={
          canShipThis
            ? [
                <Popconfirm
                  key="ship"
                  title="确认该订单已发货？"
                  onConfirm={() => void handleShip()}
                >
                  <Button type="primary" loading={shipLoading}>
                    发货
                  </Button>
                </Popconfirm>,
              ]
            : undefined
        }
      >
        {detail && (
          <>
            <ProDescriptions<Order>
              column={2}
              dataSource={detail}
              columns={DETAIL_COLUMNS}
            />
            <Table<OrderItem>
              style={{ marginTop: 16 }}
              size="small"
              rowKey={(item, index) => `${item.product_id}-${index}`}
              pagination={false}
              dataSource={detail.items}
              columns={[
                { title: '商品', dataIndex: 'product_name' },
                { title: '单价（积分）', dataIndex: 'price_snapshot' },
                { title: '数量', dataIndex: 'quantity', width: 80 },
                { title: '小计', width: 100, render: (_, item) => subtotal(item) },
              ]}
            />
          </>
        )}
      </Modal>
    </>
  );
}