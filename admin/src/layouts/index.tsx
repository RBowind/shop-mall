/**
 * ProLayout global shell.
 *
 * Renders the sidebar menu from `MENU_ITEMS` filtered by the session's
 * permission set, and the header user area (administrator name, role badge,
 * avatar dropdown for 修改密码/退出登录). Unauthenticated sessions are sent
 * to `/user/login` with `?reason=session`; signed-in sessions landing on `/`
 * are sent to the first content section their role can see. Route-level access
 * is additionally enforced by plugin-access with the same permission codes.
 */

import { useEffect, useMemo, useState } from 'react';
import { Dropdown, Avatar, Result, Space, Spin, Tag } from 'antd';
import {
  UserOutlined,
  LogoutOutlined,
  KeyOutlined,
} from '@ant-design/icons';
import { history, useModel } from '@umijs/max';
import { ProLayout } from '@ant-design/pro-components';
import type { MenuDataItem } from '@ant-design/pro-components';
import { Outlet, useLocation } from 'react-router-dom';

import { MENU_ITEMS, defaultRoutePath } from '@/menu';
import { hasPermission } from '@/access';
import { logout } from '@/services/admin';
import type { AdminSession } from '@/services/admin';
import ChangePasswordModal from '@/components/ChangePasswordModal';

interface LayoutProps {
  children: React.ReactNode;
}

interface InitialStateModel {
  initialState?: { session: AdminSession | null };
  loading?: boolean;
  setInitialState?: (state: { session: AdminSession | null }) => void;
}

/** Sidebar entries: every content section. 修改密码走头像下拉菜单, 不入侧边。 */
function sidebarMenu(permissions: readonly string[] | undefined | null): MenuDataItem[] {
  return MENU_ITEMS.filter(
    (item) =>
      item.permission === undefined || permissions?.includes(item.permission) === true,
  ).map((item) => ({ path: item.path, name: item.name }));
}

export default function BasicLayout(props: LayoutProps): React.ReactElement | null {
  // react-router v7 passes no location prop; read the router state instead.
  // Reading props.location left pathname stuck at '/' and every fresh boot on
  // a deep link (/orders etc.) was bounced back to the default section.
  const location = useLocation();
  const { initialState, loading, setInitialState } = useModel(
    '@@initialState',
  ) as InitialStateModel;
  const session = initialState?.session ?? null;
  const permissions = session?.permissions;
  const pathname = location.pathname;
  const [passwordOpen, setPasswordOpen] = useState(false);

  const menuData = useMemo(() => sidebarMenu(permissions), [permissions]);

  // Route-level guard: a menu section whose permission the session lacks
  // renders an explicit 403 instead of the page body (the backend still
  // rejects the API calls; this only makes the denial visible immediately).
  const currentEntry = useMemo(
    () => MENU_ITEMS.find((item) => item.path === pathname),
    [pathname],
  );
  const denied = currentEntry !== undefined && !hasPermission(permissions, currentEntry.permission);

  // Unauthenticated visits go to the login page (rendered without this shell).
  useEffect(() => {
    if (loading) return;
    if (!session && pathname !== '/user/login') {
      history.replace('/user/login?reason=session');
    }
  }, [loading, session, pathname]);

  // Signed-in landing on "/" goes to the first section the role can see.
  useEffect(() => {
    if (loading || !session) return;
    if (pathname === '/') {
      history.replace(defaultRoutePath(permissions));
    }
  }, [loading, session, pathname, permissions]);

  if (loading) {
    return (
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          height: '100vh',
        }}
      >
        <Spin size="large" tip="加载中…" />
      </div>
    );
  }

  if (!session) {
    return null;
  }

  const handleLogout = (): void => {
    void logout()
      .catch(() => {
        // A failed logout should not strand the UI; the client always returns
        // to the login page regardless of the backend response.
      })
      .finally(() => {
        void setInitialState?.({ session: null });
        history.replace('/user/login');
      });
  };

  const accountMenu = {
    items: [
      {
        key: 'password',
        icon: <KeyOutlined />,
        label: '修改密码',
        onClick: () => setPasswordOpen(true),
      },
      { type: 'divider' as const },
      {
        key: 'logout',
        icon: <LogoutOutlined />,
        label: '退出登录',
        onClick: handleLogout,
      },
    ],
  };

  return (
    <>
      <ProLayout
        title="Shop Mall 管理后台"
        location={{ pathname }}
        route={{ path: '/', routes: menuData }}
        menuItemRender={(item, dom) => (
          <a
            href={item.path ? `#${item.path}` : undefined}
            onClick={(e) => {
              e.preventDefault();
              if (item.path) history.push(item.path);
            }}
          >
            {dom}
          </a>
        )}
        onMenuHeaderClick={() => history.push('/')}
        avatarProps={{
          icon: <UserOutlined />,
          size: 'small',
          title: session.username,
          render: (_props, dom) => (
            <Dropdown menu={accountMenu} trigger={['click']}>
              <Space style={{ cursor: 'pointer' }}>{dom}</Space>
            </Dropdown>
          ),
        }}
        actionsRender={() => [
          <span key="role" style={{ marginRight: 12 }}>
            {session.roleName ? (
              <Tag color="blue">{session.roleName}</Tag>
            ) : (
              <span>未分配角色</span>
            )}
          </span>,
        ]}
      >
        {denied ? (
          <Result
            status="403"
            title="403"
            subTitle="无权访问该页面，请联系管理员开通对应权限"
          />
        ) : (
          <Outlet />
        )}
      </ProLayout>
      <ChangePasswordModal open={passwordOpen} onClose={() => setPasswordOpen(false)} />
    </>
  );
}