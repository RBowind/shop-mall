/**
 * 修改密码弹窗 (admin:self)。
 *
 * 单点入口：BasicLayout 头部头像下拉菜单触发。提交成功后旧会话失效，
 * 直接跳登录页让用户重新登录。
 */
import { useMemo, useState } from 'react';
import { Alert, App } from 'antd';
import { ModalForm, ProFormText } from '@ant-design/pro-components';
import { history, useModel } from '@umijs/max';

import { changePassword } from '@/services/admin';
import type { AdminSession } from '@/services/admin';
import { describeAdminError, withTraceId } from '@/requestErrorConfig';

interface Props {
  open: boolean;
  onClose: () => void;
}

const STRONG_RULE = /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d)(?=.*[^a-zA-Z0-9]).{12,}$/;

export default function ChangePasswordModal({ open, onClose }: Props): React.ReactElement {
  const { message } = App.useApp();
  const { setInitialState } = useModel('@@initialState') as {
    setInitialState?: (state: { session: AdminSession | null }) => void;
  };
  const [errorText, setErrorText] = useState('');

  const rules = useMemo(
    () => ({
      current: [{ required: true, message: '请输入当前密码' }],
      next: [
        { required: true, message: '请输入新密码' },
        {
          pattern: STRONG_RULE,
          message: '密码至少 12 位，且包含大写字母、小写字母、数字和符号',
        },
      ],
      confirm: [
        { required: true, message: '请再次输入新密码' },
      ],
    }),
    [],
  );

  return (
    <ModalForm<{ current: string; next: string; confirm: string }>
      title="修改密码"
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
      modalProps={{ destroyOnClose: true, maskClosable: false }}
      onFinish={async (values) => {
        setErrorText('');
        if (values.next !== values.confirm) {
          setErrorText('两次输入的新密码不一致');
          return false;
        }
        try {
          await changePassword(values.current, values.next);
          message.success('密码已更新，请重新登录');
          void setInitialState?.({ session: null });
          history.replace('/user/login?reason=password');
          onClose();
          return true;
        } catch (err) {
          setErrorText(withTraceId(describeAdminError(err)));
          return false;
        }
      }}
    >
      {errorText !== '' && (
        <Alert
          style={{ marginBottom: 16 }}
          showIcon
          type="error"
          message={errorText}
          role="alert"
        />
      )}
      <ProFormText.Password
        name="current"
        label="当前密码"
        fieldProps={{ autoComplete: 'current-password' }}
        rules={rules.current}
      />
      <ProFormText.Password
        name="next"
        label="新密码"
        fieldProps={{ autoComplete: 'new-password' }}
        rules={rules.next}
      />
      <ProFormText.Password
        name="confirm"
        label="确认新密码"
        fieldProps={{ autoComplete: 'new-password' }}
        rules={rules.confirm}
      />
    </ModalForm>
  );
}