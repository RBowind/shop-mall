/**
 * 积分管理页 (points:adjust)。
 *
 * 表单：买家 ID（支持 /points?user_id= 预填，会员页「调整积分」跳转过来）、
 * 带符号变动量（int64 字符串，如 "50" / "-100"）、必填备注（后端 422 校验）。
 * 提交走 POST /api/admin/v1/points/adjust，每次生成新的 Idempotency-Key：
 * 重复提交同一表单即新的调整，重放同一 key 则返回原流水条目。
 * 成功后展示返回的流水条目作为凭证。
 *
 * 管理端没有积分流水查询端点（/api/v1/points/ledger 仅限买家自查），
 * 因此本页不提供流水列表。
 */
import { useEffect, useState } from 'react';
import { Button, Card, Descriptions, Form, Input, message } from 'antd';
import { history, useAccess } from '@umijs/max';

import { adjustPoints } from '@/services/points';
import type { LedgerEntry } from '@/services/types';
import { describeAdminError, withTraceId } from '@/requestErrorConfig';

const DELTA_PATTERN = /^-?[0-9]+$/;

interface AdjustFormValues {
  user_id: string;
  delta: string;
  remark: string;
}

export default function PointsPage(): React.ReactElement {
  const access = useAccess();
  const canAdjust = access['points:adjust'] === true;

  const [form] = Form.useForm<AdjustFormValues>();
  const [submitting, setSubmitting] = useState(false);
  const [lastEntry, setLastEntry] = useState<LedgerEntry | null>(null);

  useEffect(() => {
    const userId = new URLSearchParams(history.location.search).get('user_id');
    if (userId && userId !== '') {
      form.setFieldValue('user_id', userId);
    }
  }, [form]);

  async function handleSubmit(values: AdjustFormValues): Promise<void> {
    setSubmitting(true);
    try {
      const entry = await adjustPoints({
        user_id: values.user_id.trim(),
        delta: values.delta.trim(),
        remark: values.remark.trim(),
      });
      setLastEntry(entry);
      message.success('积分调整成功');
      form.setFieldsValue({ delta: '', remark: '' });
    } catch (err) {
      message.error(withTraceId(describeAdminError(err)));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Card title="积分调整" style={{ maxWidth: 640 }}>
      {canAdjust ? (
        <>
          <Form<AdjustFormValues>
            form={form}
            layout="vertical"
            onFinish={(values) => void handleSubmit(values)}
          >
            <Form.Item
              name="user_id"
              label="买家 ID"
              rules={[
                { required: true, message: '请填写买家 ID' },
                { pattern: /^[0-9]+$/, message: '买家 ID 为数字字符串' },
              ]}
            >
              <Input placeholder="买家用户 ID（int64 数字字符串）" />
            </Form.Item>
            <Form.Item
              name="delta"
              label="变动量"
              rules={[
                { required: true, message: '请填写变动量' },
                {
                  pattern: DELTA_PATTERN,
                  message: '变动量为带符号整数，如 "50" 或 "-100"',
                },
              ]}
            >
              <Input placeholder="带符号整数，正数发放，负数扣减" />
            </Form.Item>
            <Form.Item
              name="remark"
              label="备注"
              rules={[
                { required: true, message: '备注必填，会记入流水' },
                { max: 255, message: '备注最长 255 字' },
              ]}
            >
              <Input.TextArea
                rows={2}
                placeholder="调整原因，会写入积分流水"
                maxLength={255}
              />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" loading={submitting}>
                提交调整
              </Button>
            </Form.Item>
          </Form>
        </>
      ) : (
        <Descriptions column={1}>
          <Descriptions.Item label="权限">
            当前会话没有 points:adjust 权限，仅可查看。
          </Descriptions.Item>
        </Descriptions>
      )}
      {lastEntry && (
        <Descriptions
          title="最近一次调整"
          column={1}
          bordered
          size="small"
          style={{ marginTop: 24 }}
        >
          <Descriptions.Item label="流水 ID">{lastEntry.id}</Descriptions.Item>
          <Descriptions.Item label="变动量">{lastEntry.delta}</Descriptions.Item>
          <Descriptions.Item label="调整后余额">
            {lastEntry.balance_after}
          </Descriptions.Item>
          <Descriptions.Item label="备注">{lastEntry.remark}</Descriptions.Item>
          <Descriptions.Item label="时间">
            {lastEntry.created_at}
          </Descriptions.Item>
        </Descriptions>
      )}
    </Card>
  );
}
