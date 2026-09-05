# 管理后台架构（React + Ant Design Pro）

端间契约、接口路径、鉴权方式见 `00-overview.md` 和 `../api/openapi.yaml`。本文只写管理后台内部行为。

## 1. 职责边界

管理后台是运营入口：商品上架、订单处理、发货、退款审批和权限管理。它是独立前端应用，只走 `/api/admin/v1/*`，不直连数据库。

## 2. 技术选型

| 项 | 选择 | 约定 |
|---|---|---|
| 框架 | React + Ant Design Pro | 列表、表单和权限组件复用 |
| 请求层 | 从 OpenAPI 生成 | 禁止手写重复 DTO 和 URL |
| 登录态 | HttpOnly Cookie JWT | 不把长期 token 放入 localStorage |
| 写请求 | CSRF header | Cookie 自动携带时仍需校验来源 |

## 3. 管理员初始化和认证

- `0002_seed.up.sql` 只初始化角色、权限和映射，不在 SQL 中读取密码或生成密码哈希。
- 一次性 `admin bootstrap` 命令从受控 Secret 读取用户名和初始密码，使用 Argon2id 哈希后创建超管。
- 缺少密码、密码过弱、用户名已存在或试图覆盖已有密码时命令失败。
- 初始化成功后删除或失效 bootstrap Secret。
- 登录接口为 `POST /api/admin/v1/auth/login`，失败时不区分用户名不存在和密码错误。
- 管理员 JWT 写入 `admin_access_token` HttpOnly、Secure、SameSite Cookie，有效期 30 分钟。
- 同时设置 CSRF token；所有写请求必须带 `X-CSRF-Token`。
- JWT 包含 `sub`、`iss`、`aud`、`iat`、`exp`、`jti`、`kid` 和 `token_version`，不包含权限列表。
- 改密要求输入旧密码和新密码；密码哈希更新与 token_version 递增同一事务完成。
- 改密或禁用账号后旧 JWT 立即失效，前端清理 Cookie 并回登录页。
- 登录按账号和 IP 双维度限速，成功、失败、改密和禁用事件写审计日志。

## 4. 权限模型

权限在后端中间件和 service 层双重校验，前端隐藏菜单只是体验，不是安全边界。

| 权限码 | 超管 | 运营 |
|---|---:|---:|
| product:read | ✓ | ✓ |
| product:write | ✓ | ✓ |
| image:write | ✓ | ✓ |
| order:read | ✓ | ✓ |
| order:ship | ✓ | ✓ |
| refund:read | ✓ | ✓ |
| refund:approve | ✓ | — |
| points:adjust | ✓ | — |
| role:manage | ✓ | — |
| admin:self | ✓ | ✓ |

- 未配置权限的管理员接口默认拒绝。
- 登录接口是唯一公开的管理员接口；登出和改密需要 `admin:self`。登录前没有 Cookie，因此登录是 CSRF 校验例外，依靠 HTTPS、SameSite、Origin 校验和登录限流保护。
- 角色权限不写进 JWT，每次请求按账号当前角色读取；权限修改下一次请求立即生效。
- 运营可以查看退款申请，但不能通过或驳回退款。
- 管理员只能禁用账号，不直接删除账号，避免破坏订单和流水的操作人留痕。

角色管理契约为：`GET/POST /api/admin/v1/roles`、`PATCH /api/admin/v1/roles/{roleId}`、`GET /api/admin/v1/admin-users` 和 `PATCH /api/admin/v1/admin-users/{adminUserId}`。这些写操作由 `application/access` usecase 在一个事务中完成并写审计。角色和账号变更需要 `role:manage`，不能删除仍被使用的角色，不能禁用最后一个超管，也不能通过接口删除管理员账号。

## 5. 商品和图片

- 商品编辑只接受服务端生成的图片 object key，不接受完整外部 URL。
- 图片上传接口为 `POST /api/admin/v1/images`，multipart 单文件，最大 2MB，仅 JPEG、PNG、WebP。
- 服务端校验魔数、实际解码格式、像素上限和文件大小，必要时重新编码并清理元数据。
- 文件名由服务端生成 UUID，原始文件名不进入路径。
- `products.main_image` 保存 object key；API 响应根据受信任的 `PUBLIC_BASE_URL` 生成完整 URL。
- 图片卷设置磁盘监控，替换图片不立即删除旧文件，由清理任务延迟清理未引用文件。

## 6. 订单处理

- 订单列表和详情需要 `order:read`。
- 发货需要 `order:ship`，管理员 ID 从 JWT 获取，不能由前端传入。
- 发货只允许 `paid -> shipped`，使用带原状态条件的原子 UPDATE。
- 发货写入 `shipped_by` 和 `shipped_at`；本期不采集物流单号。
- 已发货、已完成、退款申请和已退款订单不能再次发货。
- 重复发货请求返回当前状态或明确的 409，不重复修改操作人和时间。

## 7. 退款审批

- `refund:read` 只能读取待审列表和申请详情。
- `refund:approve` 同时覆盖通过和驳回；运营没有该权限。
- 审批通过必须在一个事务中完成：状态抢占、积分退回、退款流水和库存回补。
- 审批驳回只改变状态并记录审核人、审核时间和原因，不产生积分或库存副作用。
- 两个管理员并发审批时，只有成功执行 `refund_requested` 条件迁移的请求可以继续。
- 金额由后端根据订单快照计算，后台只做通过或驳回，不能编辑退款金额。

## 8. 审计与隐私

审计事件至少覆盖：登录成功/失败、改密、账号禁用、商品变更、图片上传、发货、退款审批、积分调整和权限变更。

审计字段包含操作者、操作发生时的角色快照、动作、目标、结果、时间和 trace_id。日志和页面默认脱敏手机号、地址、JWT、密码、微信 code 与 session_key。

## 9. 与小程序端的关系

两端共享 OpenAPI 契约和类型生成流程，但应用独立部署、独立鉴权、独立构建产物，不共享运行时代码。
