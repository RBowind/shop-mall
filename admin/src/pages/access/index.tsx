/**
 * 权限与账号页 (role:manage)。
 *
 * 只读两个列表：角色（含权限码清单）与管理员账号（含所属角色与启用状态）。
 * v1 的管理员创建是 bootstrap-only，API 不提供创建 / 删除 / 改角色操作，
 * 因此本页不提供写操作，只做核对与盘点。
 */
import { useEffect, useState } from 'react';
import { Card, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { listAdminUsers, listRoles } from '@/services/access';
import type { AdminUser, Role } from '@/services/types';
import { describeAdminError, withTraceId } from '@/requestErrorConfig';

const ROLE_COLUMNS: ColumnsType<Role> = [
  { title: '角色 ID', dataIndex: 'id', width: 120 },
  { title: '角色名', dataIndex: 'name', width: 180 },
  {
    title: '权限码',
    dataIndex: 'permissions',
    render: (_, role) => (
      <>
        {role.permissions.map((code) => (
          <Tag key={code}>{code}</Tag>
        ))}
      </>
    ),
  },
];

const ADMIN_USER_COLUMNS: ColumnsType<AdminUser> = [
  { title: '账号 ID', dataIndex: 'id', width: 120 },
  { title: '用户名', dataIndex: 'username', width: 200 },
  {
    title: '状态',
    dataIndex: 'enabled',
    width: 100,
    render: (_, user) =>
      user.enabled ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>,
  },
  {
    title: '角色',
    dataIndex: 'role',
    render: (_, user) => user.role?.name ?? '-',
  },
];

export default function AccessPage(): React.ReactElement {
  const [roles, setRoles] = useState<Role[]>([]);
  const [adminUsers, setAdminUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    setLoading(true);
    void Promise.all([listRoles(), listAdminUsers({ page_size: 100 })])
      .then(([roleList, adminUserPage]) => {
        setRoles(roleList);
        setAdminUsers(adminUserPage.list);
        setLoading(false);
      })
      .catch((err) => {
        message.error(withTraceId(describeAdminError(err)));
        setLoading(false);
      });
  }, []);

  return (
    <>
      <Card title="角色" style={{ marginBottom: 16 }}>
        <Table<Role>
          rowKey="id"
          size="small"
          loading={loading}
          columns={ROLE_COLUMNS}
          dataSource={roles}
          pagination={false}
        />
      </Card>
      <Card title="管理员账号">
        <Table<AdminUser>
          rowKey="id"
          size="small"
          loading={loading}
          columns={ADMIN_USER_COLUMNS}
          dataSource={adminUsers}
          pagination={false}
        />
      </Card>
    </>
  );
}
