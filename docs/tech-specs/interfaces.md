# 接口契约：shop-mall

本文件是全系统接口的详情，入口见 [`shop-mall-tech-spec.md`](./shop-mall-tech-spec.md) 第 5 节。字段级定义（请求/响应结构、schema、security scheme）的唯一契约源是 [`../api/openapi.yaml`](../api/openapi.yaml)，本文件不复制其内容，只列端点清单与决策性约束。

## 统一响应

所有业务 API 使用同一响应包裹（运维接口例外）：

```json
{ "code": 0, "data": {}, "message": "ok", "trace_id": "01J..." }
```

- `code = 0` 成功，非 0 为稳定业务错误码；HTTP 状态码表达认证、权限、资源与冲突语义。
- `trace_id`（链路追踪标识）同时经响应体与 `X-Trace-Id` 头返回；由入口中间件生成，客户端传入值仅在通过长度与字符校验时复用。
- 内部异常不把 SQL、堆栈、微信密钥与敏感个人信息返回客户端。

通用约定：分页 `page` 从 1 起、`page_size` 默认 20 上限 100，响应含 `list/total/page/page_size`；时间为 ISO 8601 UTC；积分、价格、库存、数量一律整数。

## 鉴权矩阵

| 范围 | 路径 | 鉴权 |
|---|---|---|
| 买家登录、商品与分类浏览 | `POST /api/v1/auth/wx-login`、`GET /api/v1/products*`、`GET /api/v1/categories` | 公开 |
| 买家业务 | `/api/v1/me*`、`/cart*`、`/addresses*`、`/orders*`、`/points/ledger` | 买家 Bearer JWT（JWT 为签名令牌，买家身份令牌） |
| 管理员登录 | `POST /api/admin/v1/auth/login` | 公开 |
| 管理员业务 | 其他 `/api/admin/v1/*` | 管理员 HttpOnly Cookie JWT + 权限码；写请求校验 `X-CSRF-Token`（CSRF 为跨站请求伪造） |
| 探活监控 | `/health/live`、`/health/ready`、`/metrics` | 网络访问控制，不使用业务 JWT |
| 静态图片 | `/static/images/*` | 公开只读 |

- 未列入公开清单的接口默认拒绝。
- 买家越权访问他人资源返回 404（不暴露资源是否存在）；管理员权限不足返回 403。
- 管理端写请求必须过 CSRF 校验；登录接口无 Cookie，是 CSRF 例外，靠 HTTPS、SameSite、Origin 校验与账号+IP 双维度限流保护。

权限码 × 角色矩阵（✓ 拥有）：

| 权限码 | 超管 | 运营 |
|---|---:|---:|
| `product:read` / `product:write` / `image:write` | ✓ | ✓ |
| `order:read` / `order:ship` | ✓ | ✓ |
| `refund:read` | ✓ | ✓ |
| `refund:approve` / `points:adjust` / `role:manage` | ✓ | — |
| `user:read` / `audit:read` | ✓ | — |
| `admin:self` | ✓ | ✓ |

权限实时读取不写入令牌，变更后下一次请求生效。`user:read` 与 `audit:read` 不随角色管理权自动授予。

## 错误码段

| 段 | 范围 | 覆盖 |
|---|---|---|
| 通用 | 1000-1999 | 参数、认证、权限、资源不存在 |
| 认证 | 2000-2999 | code2Session（微信登录凭证换 openid）失败、登录态过期 |
| 商品 | 3000-3999 | 商品下架、库存不足 |
| 订单 | 4000-4999 | 幂等冲突、状态迁移非法 |
| 积分 | 5000-5999 | 余额不足 |

具体码值到端点的映射以 openapi 为准。

## 买家域端点（`/api/v1`）

| 端点 | 用途 | 决策性约束 |
|---|---|---|
| `POST /auth/wx-login` | code 换买家 JWT | code 一次性；事务提交后才签发；失败归 2xxx 段；公开端点按 IP 限流 20 次/分钟（换取 openid 前无账号维度可限） |
| `GET /products` | 商品列表（仅 on_sale） | 公开；查询参数 `category`（目录键，空为全部）与 `keyword`（商品名，大小写不敏感）；按 `id DESC`，走 `(status, id)` 索引；分页 |
| `GET /categories` | 分类目录 | 公开；静态目录键 × 在售实时计数，按展示顺序返回数组，不分页 |
| `GET /products/{productId}` | 商品详情 | 公开；不存在或非在售返回 404 |
| `GET /me` | 当前买家资料与余额 | user_id 取 JWT |
| `PATCH /me` | 更新昵称 | 服务端限长度与格式 |
| `POST /me/avatar` | 更新头像 | 服务端生成图片 object key |
| `GET /cart` / `POST /cart` | 购物车列表 / 加购 | 加购同商品累加数量（`UNIQUE (user_id, product_id)` upsert） |
| `PATCH /cart/{itemId}` / `DELETE /cart/{itemId}` | 改数量 / 移除 | 归属校验，非本人 404 |
| `GET /addresses` / `POST /addresses` | 地址列表 / 新增 | 新增可带 `is_default`；首个地址不自动设默认 |
| `PATCH /addresses/{addressId}` / `DELETE /addresses/{addressId}` | 编辑、设默认 / 删除 | 设默认同事务顶掉旧默认；非本人 404 |
| `POST /orders` | 下单（幂等） | Header `Idempotency-Key`（UUID 幂等键，落库为 `client_token`）；只提交购物车项与地址 ID；同键同 hash 重放原订单 200，异 hash 409 |
| `GET /orders` / `GET /orders/{orderId}` | 订单列表 / 详情 | 归属校验；列表按 `(user_id, status, created_at)` |
| `POST /orders/{orderId}/refund` | 申请退款 | 仅 `paid` 可申；迁移失败 404/409 |
| `POST /orders/{orderId}/confirm` | 确认收货 | 仅 `shipped` 可确认 |
| `GET /points/ledger` | 本人积分流水 | 只读，按 `(user_id, id DESC)` |

## 管理员域端点（`/api/admin/v1`）

| 端点 | 用途 | 权限码 | 决策性约束 |
|---|---|---|---|
| `POST /auth/login` | 登录 | 公开 | 失败不区分账号不存在/密码错误；下发 Cookie 与 CSRF token |
| `POST /auth/logout` | 登出 | `admin:self` | 清理 Cookie |
| `POST /auth/password` | 改密 | `admin:self` | 验旧密码；哈希更新与 `token_version` 递增同事务 |
| `GET /products` / `POST /products` | 商品列表 / 新建 | `product:read` / `product:write` | 新建默认 `on_sale` |
| `GET /products/{productId}` / `PATCH /products/{productId}` | 详情 / 编辑含上下架 | `product:read` / `product:write` | 图片只接受服务端 object key 数组；`category` 限目录键，未知键 422 |
| `POST /images` | 上传商品图 | `image:write` | multipart 单文件 ≤ 2MB，仅 JPEG/PNG/WebP，校验魔数与解码；UUID key |
| `GET /orders` / `GET /orders/{orderId}` | 全量订单查询 | `order:read` | 列表含收货信息，按脱敏规则展示 |
| `POST /orders/{orderId}/ship` | 发货 | `order:ship` | 仅 `paid → shipped`；`shipped_by` 取 JWT |
| `GET /refunds` | 退款待审列表 | `refund:read` | 只读 |
| `POST /refunds/{orderId}/approve` | 审批通过 | `refund:approve` | 条件抢占后同事务退积分、回补库存；未抢到 409 |
| `POST /refunds/{orderId}/reject` | 审批驳回 | `refund:approve` | 仅回退状态与留痕，无账务副作用 |
| `POST /points/adjust` | 积分调整 | `points:adjust` | `Idempotency-Key`；`event_key = admin_adjust:{admin_id}:{key}`；同键异 hash 409；remark 必填 |
| `GET /roles` / `POST /roles` | 角色列表 / 新建 | `role:manage` | 写操作同事务写审计 |
| `PATCH /roles/{roleId}` | 角色授权变更 | `role:manage` | 不能删除仍被使用的角色 |
| `GET /admin-users` / `PATCH /admin-users/{adminUserId}` | 账号列表 / 禁用、改角色 | `role:manage` | 只禁用不删除；不能禁用最后一个超管；禁用递增 `token_version` |
| `GET /users` | 买家列表 | `user:read` | 只读；`keyword` 数字匹配买家 ID，否则匹配昵称 |
| `GET /audit-logs` | 审计日志查询 | `audit:read` | 只读，脱敏摘要 |

## 运维端点

| 端点 | 用途 | 约束 |
|---|---|---|
| `GET /health/live` | 进程存活探测 | 不走业务 JWT 与统一包裹；仅探活网络可访问 |
| `GET /health/ready` | 依赖就绪探测（数据库等） | 同上 |
| `GET /metrics` | Prometheus 指标暴露 | 仅监控网络可访问；trace_id 不作为指标标签 |

## 跨端约定

- 请求层的 TypeScript / Go 类型由 openapi 生成，生成代码禁止手改；契约变更先改 openapi 再重新生成。
- 图片完整 URL 由服务端按受信任的 `PUBLIC_BASE_URL` 拼接返回，客户端不自行拼域名；商品视图字段 `images` 为图集 URL 数组，`main_image` 为首图派生 URL（兼容旧客户端，非存储列）。
- 下单请求体不携带价格、数量、库存与 user_id，全部以服务端事务内读取为准。
