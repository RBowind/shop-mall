/**
 * Login page.
 *
 * ProForm sign-in. On success the backend sets the HttpOnly session cookie and
 * the readable csrf_token cookie (the transport injects CSRF on later writes);
 * the returned session is written into the app initial state, which flips the
 * permission bits and mounts the ProLayout shell at the first section the role
 * can see. Validation mirrors the backend rules (3-32 char username, password
 * >= 12 chars with upper/lower/digit/symbol); failures surface the server
 * message with its trace id.
 */

import { useMemo, useState } from 'react';
import { Alert } from 'antd';
import { history, useModel } from '@umijs/max';
import { useLocation } from 'react-router-dom';
import { LoginForm, ProFormText } from '@ant-design/pro-components';

import { login, withPermissions } from '@/services/admin';
import type { AdminSession } from '@/services/admin';
import { listRoles } from '@/services/access';
import { describeAdminError, isApiRequestError, withTraceId } from '@/requestErrorConfig';
import { defaultRoutePath } from '@/menu';

/** Backend ValidateUsername / ValidatePassword surface rules. */
const USERNAME_RULE = /^.{3,32}$/;
const PASSWORD_RULE = /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d)(?=.*[^a-zA-Z0-9]).{12,}$/;

interface LoginValues {
  username?: string;
  password?: string;
}

export default function LoginPage(): React.ReactElement {
  const { setInitialState } = useModel('@@initialState') as {
    setInitialState?: (state: { session: AdminSession | null }) => void;
  };
  const [errorText, setErrorText] = useState('');
  // Arriving with ?reason=session means the transport bounced an expired
  // session here; say so once instead of silently showing a bare form.
  const query = new URLSearchParams(useLocation().search);
  const sessionExpired = query.get('reason') === 'session';

  const rules = useMemo(
    () => ({
      username: [
        { required: true, message: '请输入用户名' },
        { pattern: USERNAME_RULE, message: '用户名长度为 3-32 位' },
      ],
      password: [
        { required: true, message: '请输入密码' },
        {
          pattern: PASSWORD_RULE,
          message: '密码至少 12 位，且包含大写字母、小写字母、数字和符号',
        },
      ],
    }),
    [],
  );

  async function onFinish(values: LoginValues): Promise<boolean> {
    setErrorText('');
    try {
      const base = await login(values.username ?? '', values.password ?? '');
      let session: AdminSession = base;
      try {
        session = await withPermissions(base, listRoles);
      } catch {
        // The roles endpoint needs role:manage; an operator keeps the session
        // as-is, which is fine — the menu renders conservatively.
      }
      void setInitialState?.({ session });
      // Full reload instead of an in-SPA jump: the initial-state model
      // propagates through an executor effect, and a route change batched
      // with setInitialState lets the freshly mounted layout read a stale
      // null session and bounce the user straight back to the login page.
      // Reloading re-runs getInitialState against the just-set session
      // cookie, which is deterministic.
      history.replace(defaultRoutePath(session.permissions));
      window.location.reload();
      return true;
    } catch (err) {
      // A 401 from the login call itself is a credential rejection; the
      // generic session-expired wording would be misleading here.
      if (isApiRequestError(err) && err.statusCode === 401) {
        setErrorText('用户名或密码错误');
      } else {
        const presentation = describeAdminError(err);
        setErrorText(presentation.redirectToLogin ? '登录状态已失效，请重新登录' : withTraceId(presentation));
      }
      return false;
    }
  }

  return (
    <LoginForm
      title="Shop Mall 管理后台"
      subTitle="管理员登录"
      onFinish={onFinish}
      initialValues={{ username: '', password: '' }}
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
      {errorText === '' && sessionExpired && (
        <Alert
          style={{ marginBottom: 16 }}
          showIcon
          type="info"
          message="登录状态已失效，请重新登录"
          role="status"
        />
      )}
      <ProFormText
        name="username"
        label="用户名"
        placeholder="请输入用户名"
        rules={rules.username}
        fieldProps={{ size: 'large', autoComplete: 'username' }}
      />
      <ProFormText.Password
        name="password"
        label="密码"
        placeholder="至少 12 位，含大小写字母、数字、符号"
        rules={rules.password}
        fieldProps={{ size: 'large', autoComplete: 'current-password' }}
      />
    </LoginForm>
  );
}