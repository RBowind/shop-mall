# 后端架构（Go + Gin + GORM）

端间契约、接口分组、鉴权方式和订单状态机见 `00-overview.md` 与 `../api/openapi.yaml`，本文只写后端内部边界。

## 1. 职责边界

后端是唯一的业务规则所在地。价格计算、库存扣减、积分账本、订单状态机、对象归属和管理员权限全部在后端完成，且有副作用的操作必须在数据库事务里完成。前端和管理后台都是后端的哑终端。

## 2. 模块划分

目录即边界，模块之间禁止跨模块直调对方 repository。跨模块流程由 `application` usecase 编排。

```text
internal/
├── application/       # 跨模块用例和事务边界
│   ├── auth/           # 登录、用户创建、首次赠分
│   ├── order/          # 下单、发货、确认收货
│   ├── refund/         # 退款申请、审批、驳回
│   ├── points/         # 管理员积分调整
│   └── access/         # 角色、权限和管理员账号变更
├── auth/               # JWT、管理员 Cookie、账号状态
├── user/               # 用户档案、地址、余额查询
├── product/            # 商品读写、库存和图片 key
├── cart/               # 购物车增删改查
├── order/              # 订单领域规则和状态机
├── payment/            # 积分账本、余额变动和对账
├── admin/              # 管理员接口、角色权限、bootstrap
├── storage/            # 本地 volume / 对象存储适配器
├── platform/wechat/    # code2Session，禁止泄露 session_key
└── middleware/         # JWT、CSRF、traceId、限流、恢复
```

## 3. 分层与事务

```text
handler（Gin 路由层）
  -> application/usecase（事务编排）
       -> service（业务规则）
            -> repository（GORM 数据访问）
```

- handler 只做参数解析、基础格式校验和响应包裹。
- application usecase 是唯一的事务入口，使用 `db.Transaction` 管理提交和回滚。
- service 接收调用方传入的 `*gorm.DB`，不得自行开启、提交或回滚外层事务。
- repository 只执行数据访问，不决定业务流程和错误码。
- 外部网络调用必须在事务外完成；事务提交后才签发 token 或返回成功响应。

### 3.1 事务锁顺序

涉及多个资源时统一遵守：

```text
订单行（按 user + 幂等 token 定位；下单时即前置重放检查）
  -> 用户行
  -> 地址与购物车行
  -> 商品行，按 product_id 升序
```

下单与订单状态类操作共用此锁顺序。用户余额使用行锁和条件更新双重保护；商品库存使用条件更新防止超卖。

### 3.2 数据库重试

事务执行器只在完整事务边界重试 PostgreSQL `40P01`（deadlock detected）和 `40001`（serialization failure）。重试必须重新读取、重新计算和重新执行整个 usecase，不能重试单条 UPDATE，也不能复用已经失效的 GORM transaction。

## 4. 登录与首次赠分

```text
code2Session（事务外）
  -> 开启事务
  -> INSERT users ... ON CONFLICT DO NOTHING RETURNING id
  -> 只有真正插入用户的请求执行 signup_bonus
  -> 更新 points_balance + 写 points_ledger
  -> 提交事务
  -> 签发买家 JWT
```

- `code` 只能被微信消费一次；网络超时不能盲目重放同一个 code。
- `session_key` 本期不持久化、不下发、不写日志。
- `SIGNUP_BONUS_POINTS=0` 时不更新余额、不写流水。
- 新用户创建、余额增加和 `signup_bonus` 流水必须同一事务。
- `signup_bonus` 由数据库部分唯一索引和应用流程共同保证每用户最多一次。

## 5. 下单用例

下单 usecase 的步骤必须固定：

1. 从 JWT 取得 `user_id`，忽略请求体中的用户 ID。
2. 开启事务，`SELECT ... FOR UPDATE` 查询并锁定 `(user_id, client_token)` 已有订单。
3. 已存在时比较 `request_hash`：相同则返回原订单，不执行任何副作用；不同则返回幂等冲突。
4. 无已有订单时锁定用户行。
5. 校验并锁定地址与所选购物车项归属当前用户。
6. 按 `product_id` 升序锁商品，读取最新价格和库存。
7. 普通 INSERT 创建订单，写入明细，保存价格、商品资料和地址快照。
8. 使用条件更新扣库存。
9. 使用条件更新扣积分并取得 `balance_after`。
10. 写入唯一 `event_key` 的 `order_pay` 流水，删除所选购物车项。
11. 提交事务后返回订单。

同 token 的两个首次并发请求都能通过第 2 步前置检查；后到者的 INSERT 撞 `(user_id, client_token)` 唯一约束后整体回滚，在事务外重读获胜订单，按 `request_hash` 返回重放或幂等冲突。业务失败回滚后不固化 token。

## 6. 退款、发货和确认收货

- 退款申请使用 `UPDATE orders ... WHERE id=? AND user_id=? AND status='paid'`，影响行数为 0 时返回 404 或 409。
- 退款审批先条件迁移 `refund_requested -> refunded`，只有成功抢占状态的请求才能恢复积分、写流水和回补库存。
- 退款驳回条件迁移 `refund_requested -> paid`，不产生积分和库存副作用。
- 发货使用 `paid -> shipped` 条件更新，并从管理员 JWT 取得 `shipped_by`，不信任请求体中的管理员 ID。
- 确认收货使用 `shipped -> completed` 条件更新，并追加 `user_id` 归属条件。
- 重复审批、重复发货和重复确认不能重复写时间、流水、库存或余额。

## 7. 积分账本

- `users.points_balance` 是实时扣减权威；`points_ledger` 是追加式审计账本。
- 每个积分事件必须有服务端生成的唯一 `event_key`。
- `order_pay` 为负数，`order_refund` 和 `signup_bonus` 为正数，`admin_adjust` 可正可负但必须通过余额条件更新。
- `admin_adjust` 必须有管理员 ID和非空备注；管理员不能直接修改余额字段。
- 管理员积分调整使用 `Idempotency-Key` 作为请求幂等键；服务端派生 `event_key = admin_adjust:{admin_id}:{idempotency_key}`（幂等域为发起操作的管理员），并保存规范化请求的 `request_hash`；同 key 同 hash 重放原流水，不同 hash 返回 409。
- 余额更新和流水写入必须同一事务，流水的 `balance_after` 使用余额 UPDATE 的 `RETURNING` 值。
- 定期对账 `SUM(points_ledger.delta)` 与 `users.points_balance`；不一致只报警和冻结相关运营操作，不自动改账。

角色和管理员账号变更由 `application/access` usecase 编排：角色变更同时写 `roles`、`role_permissions` 和 `audit_logs`；管理员变更同时写 `admin_users`、必要的 `token_version` 和 `audit_logs`。这些操作使用同一事务，锁顺序为管理员行 -> 角色行 -> 权限映射，并按 `40P01/40001` 重试完整事务。

## 8. 鉴权、错误和日志

- 买家和管理员使用不同密钥、不同 audience 和不同中间件。
- 买家资源必须在 service 层按 `user_id` 做对象归属校验。
- 管理员中间件校验签名算法、issuer、audience、过期时间、kid、账号 enabled 和 token_version；权限实时读取。
- 错误码和 HTTP 状态以 OpenAPI 为准；内部错误不暴露数据库和第三方详情。
- traceId 由入口生成；客户端传入的 `X-Trace-Id` 只有通过长度和字符校验才复用，否则重新生成。
- traceId 写入 request context、应用日志、响应体和 `X-Trace-Id` 响应头，不作为 Prometheus label。
- `/health/live`、`/health/ready` 和 `/metrics` 是运维接口例外，不使用统一业务响应包裹；它们通过网络访问控制保护。
- 日志禁止记录密码、JWT、微信 code、session_key、完整手机号和收货地址。
- 管理员敏感操作写入 `audit_logs`，记录操作发生时的角色快照、动作、目标、结果、脱敏前后摘要和 trace_id；审计表只追加。

## 9. 图片存储

- `POST /api/admin/v1/images` 需要管理员 JWT 和 `image:write`。
- 应用和 nginx 同时限制 2MB 请求体；只接受 JPEG、PNG、WebP。
- 校验魔数和真实解码结果，限制像素数，必要时重新编码并清理元数据。
- 文件由服务端生成 UUID key，数据库保存 object key，不保存完整 URL。
- `storage` 接口隔离本地 volume 与未来对象存储；上传失败和未关联文件由清理任务处理。

## 10. 工程约定

| 项 | 约定 |
|---|---|
| 迁移 | goose 显式迁移，已执行 migration 禁止修改，生产只做 forward migration |
| 校验 | handler 做格式校验，service 做归属和业务校验，事务层做一致性校验 |
| 密码 | Argon2id 编码，明文不进 SQL、镜像和日志 |
| 监控 | `/metrics` 只允许监控网络访问，探活拆为 live/ready |
| 限流 | 管理员登录按账号和 IP 双维度限流 |
| 配置 | 启动时一次性加载并校验，生产缺少 Secret 时 fail closed；JWT 使用带 active kid 的 keyring |
