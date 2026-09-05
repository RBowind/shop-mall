# 数据模型：shop-mall

本文件是全系统数据模型的详情，入口见 [`shop-mall-tech-spec.md`](./shop-mall-tech-spec.md)。字段、类型与约束来自 [`../architecture/05-database.md`](../architecture/05-database.md)（建表级设计，权威 DDL 为其 goose migration），此处按表整理字段 shape 与决策性约束，不复制完整 DDL。

## 设计原则

- 积分、价格、库存、数量一律整数；积分与余额用 BIGINT。
- 订单保存商品与地址快照，历史订单不依赖可变的商品与地址。
- 状态用 VARCHAR + CHECK，合法迁移在服务层条件更新中保证。
- 账务流水只追加、不修改、不删除；修正通过反向或调整流水完成。
- 生产回滚只回滚应用，不使用 goose down 删除生产数据。

写入语义按表标注（insert / upsert / append / 条件更新）。

## 权限域（先建，供订单与流水外键引用）

### roles / permissions / role_permissions

| 表 | 关键字段 | 约束与写入语义 |
|---|---|---|
| `roles` | `id`、`name`、`remark` | `name` 唯一；insert |
| `permissions` | `id`、`code`、`name` | `code` 唯一；insert |
| `role_permissions` | `role_id`、`permission_id` | 复合主键；级联删除；insert |

权限码枚举（12 项，全量）：`product:read`、`product:write`、`image:write`、`order:read`、`order:ship`、`refund:read`、`refund:approve`、`points:adjust`、`role:manage`、`admin:self`、`user:read`、`audit:read`。其中 `user:read`（买家列表）与 `audit:read`（审计查询）仅授予超管——角色管理权不自动带出成员与审计可见性。

### admin_users

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGINT | 自增主键 | PK |
| `username` | VARCHAR(32) | 请求/上游输入 | 唯一 |
| `password_hash` | VARCHAR(255) | 服务自行生成 | Argon2id 哈希，明文不入库 |
| `token_version` | BIGINT | 服务维护 | 默认 1，> 0；改密或禁用递增使旧令牌失效 |
| `role_id` | BIGINT | 请求 | 外键 roles |
| `enabled` | BOOLEAN | 请求 | 默认 true；只能禁用不删除 |
| `last_login_at` | TIMESTAMPTZ | 服务自行生成 | 登录时更新 |

写入语义：仅 bootstrap 命令 insert；账号管理接口只 GET/PATCH（禁用、改角色为条件 UPDATE，改密为 password_hash 更新与 token_version 递增同一事务），v1 不提供创建管理员的接口。

## 买家域

### users

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGSERIAL | 自增 | PK |
| `openid` | VARCHAR(64) | 上游（微信 code2Session） | 唯一，当前小程序内登录键 |
| `nickname` | VARCHAR(64) | 用户输入 | 默认空 |
| `avatar_url` | TEXT | 用户输入 | object key，默认空 |
| `points_balance` | BIGINT | 服务派生 | `>= 0`；实时扣减权威，与流水同事务双写 |

写入语义：登录时按 openid `INSERT ... ON CONFLICT DO NOTHING RETURNING id`（insert-once）；余额变动为条件 UPDATE（`WHERE points_balance >= amount RETURNING`）。业务表只经 `user_id` 关联，不把 openid 当内部外键。

### user_addresses

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGSERIAL | 自增 | PK |
| `user_id` | BIGINT | JWT 派生 | 外键 users，级联删除 |
| `receiver` | VARCHAR(32) | 用户输入 | 非空 |
| `phone` | VARCHAR(20) | 用户输入 | 非空，敏感字段 |
| `region` | VARCHAR(64) | 用户输入 | 非空 |
| `detail` | VARCHAR(255) | 用户输入 | 非空，敏感字段 |
| `is_default` | BOOLEAN | 用户输入 | 默认 false |
| `version` | BIGINT | 服务维护 | 创建为 1，每次编辑递增，`> 0`；是下单 `request_hash` 的输入，客户端只透传不修改 |

决策性约束：部分唯一索引 `ON (user_id) WHERE is_default = true` 保证一人至多一个默认；设默认时同事务先取消旧默认再置新，部分唯一索引为并发兜底。列表查询走 `(user_id, id)` 索引。写入语义：新增 insert、编辑/设默认 partial update（递增 version）、删除 delete。

### cart_items

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGSERIAL | 自增 | PK |
| `user_id` | BIGINT | JWT 派生 | 外键 users，级联删除 |
| `product_id` | BIGINT | 用户输入 | 外键 products |
| `quantity` | INT | 用户输入 | `> 0` |

`UNIQUE (user_id, product_id)`：重复加购由服务累加 quantity（upsert 语义）。商品下架不删购物车记录，读取时提示不可购买。

## 商品域

### products

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGSERIAL | 自增 | PK |
| `name` | VARCHAR(128) | 管理员输入 | 非空 |
| `description` | TEXT | 管理员输入 | 默认空 |
| `category` | VARCHAR(32) | 管理员输入 | 默认空（未分类）；取值限静态目录键 `digital` \| `home` \| `beauty` \| `food` \| `apparel`，未知键 422 |
| `images` | JSONB | 管理员输入（object key 数组） | 有序图集，首图即主图；`NOT NULL DEFAULT '[]'`；只存 object key 不存完整 URL |
| `price_points` | BIGINT | 管理员输入 | `> 0` |
| `stock` | INT | 服务维护 | `>= 0`；条件 UPDATE 扣减防超卖 |
| `status` | VARCHAR(16) | 管理员输入 | 枚举 `on_sale` \| `off_sale`，默认 `on_sale` |

API 响应中的 `main_image` 是图集首图派生的公共 URL（兼容旧客户端），不是存储列。分类目录是产品模块代码中的静态枚举（键 + 中文名 + 展示顺序），`GET /categories` 将其与在售商品实时计数配对，不建目录表。

商品用下架代替物理删除，避免订单明细失去引用。公开列表走 `(status, id DESC)` 索引，按 `id DESC` 排序。

## 交易域

### orders

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGSERIAL | 自增 | PK |
| `order_no` | VARCHAR(32) | 服务生成 | 唯一 |
| `user_id` | BIGINT | JWT 派生 | 外键 users |
| `client_token` | VARCHAR(64) | 客户端输入（幂等键） | 非空且去空格非空 |
| `request_hash` | CHAR(64) | 服务派生 | 对所选购物车项 ID、每项商品与数量、地址 ID 与地址 version 规范化后 SHA-256；价格与库存不进入 hash |
| `status` | VARCHAR(20) | 服务迁移 | 枚举 `paid` \| `shipped` \| `completed` \| `refund_requested` \| `refunded` |
| `total_points` | BIGINT | 服务派生 | `> 0` |
| `receiver` | VARCHAR(32) | 地址快照 | 非空 |
| `phone` | VARCHAR(20) | 地址快照 | 非空，敏感 |
| `address` | VARCHAR(512) | 地址快照 | 非空，敏感 |
| `refund_reason` | VARCHAR(255) | 买家申请 | 默认空 |
| `refund_reject_reason` | VARCHAR(255) | 管理员驳回 | 默认空 |
| `refund_reviewed_by` | BIGINT | 管理员 JWT | 外键 admin_users，RESTRICT |
| `refund_reviewed_at` | TIMESTAMPTZ | 服务生成 | — |
| `paid_at` | TIMESTAMPTZ | 服务生成 | 默认 now |
| `shipped_by` | BIGINT | 管理员 JWT | 外键 admin_users，RESTRICT |
| `shipped_at` / `completed_at` / `refunded_at` | TIMESTAMPTZ | 服务生成 | 见状态一致性 CHECK |

决策性约束：
- `UNIQUE (user_id, client_token)`：下单幂等键。`request_hash` 的规范化编码发布后冻结，变更必须兼容存量订单的重放判定，否则旧 token 重放会全部误判 409。
- 状态与时间戳一致性 CHECK：`shipped`/`completed` 必同时有 `shipped_by` 与 `shipped_at`，非此二状态二者必为空；`status = 'completed'` 与 `completed_at IS NOT NULL` 等价；`status = 'refunded'` 与 `refunded_at IS NOT NULL` 等价。
- 索引：`(user_id, status, created_at DESC, id DESC)` 供买家订单列表，`(status, created_at DESC, id DESC)` 供后台按状态查询。

写入语义：下单为普通 INSERT，`(user_id, client_token)` 唯一约束是并发兜底，前置重放检查与冲突兜底见 [`flows.md`](./flows.md) 第 2 节（成功事务才固化 token，业务失败整体回滚）；状态迁移全部为带原状态条件的 UPDATE。

### order_items

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGSERIAL | 自增 | PK |
| `order_id` | BIGINT | 派生 | 外键 orders，级联删除 |
| `product_id` | BIGINT | 请求 | 外键 products |
| `product_name` | VARCHAR(128) | 商品快照 | 非空 |
| `product_image` | TEXT | 商品快照 | 默认空 |
| `price_snapshot` | BIGINT | 商品快照 | `> 0` |
| `quantity` | INT | 请求 | `> 0` |

`UNIQUE (order_id, product_id)`，`(order_id)` 索引。写入语义：随订单 insert，快照创建后不更新。

## 账务域

### points_ledger

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGSERIAL | 自增 | PK |
| `user_id` | BIGINT | 派生 | 外键 users |
| `order_id` | BIGINT | 派生 | 外键 orders，可空 |
| `event_key` | VARCHAR(96) | 服务生成 | 唯一 |
| `request_hash` | CHAR(64) | 服务派生 | 仅 `admin_adjust` 非空 |
| `type` | VARCHAR(20) | 服务 | 枚举 `order_pay` \| `order_refund` \| `signup_bonus` \| `admin_adjust` |
| `delta` | BIGINT | 服务派生 | `<> 0` |
| `balance_after` | BIGINT | 余额 UPDATE 的 RETURNING | `>= 0` |
| `created_by_admin_id` | BIGINT | 管理员 JWT | 外键 admin_users，RESTRICT |
| `remark` | VARCHAR(255) | 管理员输入 | `admin_adjust` 非空 |

写入语义：append-only，只插不改不删。

事件类型与字段联动 CHECK（四类各自的 order_id / delta 方向 / 管理员留痕组合，完整见 05-database.md）：

- `order_pay`：关联订单、delta < 0、无管理员。
- `order_refund`：关联订单、delta > 0、有管理员。
- `signup_bonus`：不关联订单、delta > 0、无管理员。
- `admin_adjust`：不关联订单、有管理员、remark 非空、request_hash 非空。

业务事件键由服务端代码生成，客户端不参与拼接：`signup:{user_id}`、`order_pay:{order_id}`、`order_refund:{order_id}`、`admin_adjust:{admin_id}:{idempotency_key}`——`admin_adjust` 的幂等域是发起操作的管理员——同一 `Idempotency-Key` 由不同管理员分别提交时事件键互不相同、不互斥，去重保护的范围是单个发起者。

部分唯一索引（event_key 生成错误时的记账兜底）：`signup_bonus` 每人一条、`order_pay` 每单一条、`order_refund` 每单一条。索引：`(user_id, id DESC)`、`(order_id)`。

### audit_logs

| 字段 | 类型 | 来源 | 约束 |
|---|---|---|---|
| `id` | BIGSERIAL | 自增 | PK |
| `actor_admin_id` | BIGINT | 管理员 JWT | 外键 admin_users，RESTRICT |
| `actor_role` | VARCHAR(32) | 审计时角色快照 | 非空 |
| `action` | VARCHAR(64) | 服务 | 非空 |
| `target_type` | VARCHAR(64) | 服务 | 非空 |
| `target_id` | BIGINT | 服务 | 可空 |
| `result` | VARCHAR(16) | 服务 | 枚举 `success` \| `failure` |
| `before_data` / `after_data` | JSONB | 服务 | 只存脱敏后摘要 |
| `trace_id` | VARCHAR(128) | 入口中间件生成 | 非空 |

写入语义：append-only。事件范围至少覆盖：登录成功/失败、改密、禁用账号、商品变更、图片上传、发货、退款审批、积分调整、权限变更。索引：`(actor_admin_id, created_at DESC)`、`(target_type, target_id, created_at DESC)`。
