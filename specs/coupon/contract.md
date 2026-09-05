# Sprint Contract: coupon-pay-lifecycle

Source:
- `specs/coupon/spec.md`（ADDED）
- `specs/order-payment/spec.md`（ADDED）
- `specs/checkout/spec.md`（`## MODIFIED Requirements`）
- `specs/points/spec.md`（`## MODIFIED Requirements`）
- `specs/fulfillment/spec.md`（`## MODIFIED Requirements`）
- `specs/refund/spec.md`（`## MODIFIED Requirements`）

Status: DRAFT（人 gate 未放行，devloop 不可启动）

## Behavioral（from spec scenarios，1:1 抽取）

### specs/coupon/spec.md（新增）
- [ ] B1: 创建券模板 / 创建成功 — verified by BDD test
- [ ] B2: 创建券模板 / 规则非法 — verified by BDD test
- [ ] B3: 创建券模板 / 无权限创建 — verified by BDD test
- [ ] B4: 创建券模板 / 规则创建后不可修改 — verified by BDD test
- [ ] B5: 切换发放状态 / 停发 — verified by BDD test
- [ ] B6: 切换发放状态 / 恢复发放 — verified by BDD test
- [ ] B7: 切换发放状态 / 切换留痕 — verified by BDD test
- [ ] B8: 管理员查看模板 / 模板列表 — verified by BDD test
- [ ] B9: 管理员查看模板 / 无权限查看 — verified by BDD test
- [ ] B10: 领券中心可见性 / 列表内容 — verified by BDD test
- [ ] B11: 领券中心可见性 / 不可领模板不展示 — verified by BDD test
- [ ] B12: 领取优惠券 / 领取成功 — verified by BDD test
- [ ] B13: 领取优惠券 / 达到每人限领 — verified by BDD test
- [ ] B14: 领取优惠券 / 模板售罄与并发不超发 — verified by BDD test
- [ ] B15: 领取优惠券 / 模板不可领 — verified by BDD test
- [ ] B16: 领取优惠券 / 幂等重放 — verified by BDD test
- [ ] B17: 领取优惠券 / 并发双击领取 — verified by BDD test
- [ ] B18: 券生命周期状态机 / 下单占用与释放后可复用 — verified by BDD test
- [ ] B19: 券生命周期状态机 / 一张券至多被一笔在途订单占用 — verified by BDD test
- [ ] B20: 券生命周期状态机 / 过期扫描只动未占用的券 — verified by BDD test
- [ ] B21: 券生命周期状态机 / 已过期券不再被任何路径复用 — verified by BDD test
- [ ] B22: 买家查询我的券 / 按状态查券 — verified by BDD test
- [ ] B23: 买家查询我的券 / 只返回本人的券 — verified by BDD test

### specs/order-payment/spec.md（新增）
- [ ] B24: 确认支付 / 支付成功 — verified by BDD test
- [ ] B25: 确认支付 / 余额不足 — verified by BDD test
- [ ] B26: 确认支付 / 非待支付状态支付 — verified by BDD test
- [ ] B27: 确认支付 / 并发与重复支付只成交一次 — verified by BDD test
- [ ] B28: 确认支付 / 支付他人订单 — verified by BDD test
- [ ] B29: 支付时限 / 时限随订单生成 — verified by BDD test
- [ ] B30: 支付时限 / 取消处理前仍可支付 — verified by BDD test
- [ ] B31: 超时自动取消 / 超时订单被取消 — verified by BDD test
- [ ] B32: 超时自动取消 / 取消与支付并发只有一方生效 — verified by BDD test
- [ ] B33: 超时自动取消 / 同一订单重复处理只释放一次 — verified by BDD test
- [ ] B34: 超时自动取消 / 处理失败不漏单 — verified by BDD test
- [ ] B35: 超时自动取消 / 取消是终态 — verified by BDD test
- [ ] B36: 过期券清理任务 / 过期券被置终态 — verified by BDD test

### specs/checkout/spec.md（MODIFIED）
- [ ] B37: MODIFIED 服务端定价下单 / 下单成功 — verified by BDD test
- [ ] B38: MODIFIED 服务端定价下单 / 余额不足不再是下单门槛 — verified by BDD test
- [ ] B39: MODIFIED 服务端定价下单 / 券资格服务端判定 — verified by BDD test
- [ ] B40: MODIFIED 服务端定价下单 / 请求体不携带价格与身份 — verified by BDD test
- [ ] B41: MODIFIED 服务端定价下单 / 库存不足 — verified by BDD test
- [ ] B42: MODIFIED 服务端定价下单 / 提交含已下架商品的行 — verified by BDD test
- [ ] B43: MODIFIED 服务端定价下单 / 提交非本人的行或地址 — verified by BDD test
- [ ] B44: MODIFIED 下单幂等 / 原样重放 — verified by BDD test
- [ ] B45: MODIFIED 下单幂等 / 同键不同内容 — verified by BDD test
- [ ] B46: MODIFIED 下单幂等 / 内容一致的判定范围 — verified by BDD test
- [ ] B47: MODIFIED 下单幂等 / 并发双击提交 — verified by BDD test
- [ ] B48: MODIFIED 买家订单查询 / 按状态过滤 — verified by BDD test
- [ ] B49: MODIFIED 买家订单查询 / 列表不带状态参数 — verified by BDD test
- [ ] B50: MODIFIED 买家订单查询 / 订单详情内容 — verified by BDD test
- [ ] B51: MODIFIED 买家订单查询 / 查询他人订单 — verified by BDD test

### specs/points/spec.md（MODIFIED）
- [ ] B52: MODIFIED 余额与流水同事务双写 / 任一积分变动 — verified by BDD test
- [ ] B53: MODIFIED 余额恒不为负 / 扣成负数 — verified by BDD test

### specs/fulfillment/spec.md（MODIFIED）
- [ ] B54: MODIFIED 管理员发货 / 发货成功 — verified by BDD test
- [ ] B55: MODIFIED 管理员发货 / 状态不符 — verified by BDD test
- [ ] B56: MODIFIED 管理员发货 / 订单不存在 — verified by BDD test
- [ ] B57: MODIFIED 管理员发货 / 无权限管理员 — verified by BDD test
- [ ] B58: MODIFIED 买家确认收货 / 确认成功 — verified by BDD test
- [ ] B59: MODIFIED 买家确认收货 / 其他状态确认 — verified by BDD test
- [ ] B60: MODIFIED 买家确认收货 / 重复确认 — verified by BDD test
- [ ] B61: MODIFIED 买家确认收货 / 确认他人订单 — verified by BDD test
- [ ] B62: MODIFIED 管理端订单查询 / 列表查询 — verified by BDD test
- [ ] B63: MODIFIED 管理端订单查询 / 详情查询 — verified by BDD test
- [ ] B64: MODIFIED 管理端订单查询 / 无权限查询 — verified by BDD test
- [ ] B65: MODIFIED 管理端订单查询 / 查询不存在的订单 — verified by BDD test

### specs/refund/spec.md（MODIFIED，本 feature 修订项）
- [ ] B66: MODIFIED 申请退款仅限未发货订单 / 申请成功 — verified by BDD test
- [ ] B67: MODIFIED 申请退款仅限未发货订单 / 已发货订单申请 — verified by BDD test
- [ ] B68: MODIFIED 申请退款仅限未发货订单 / 其他状态申请（含 pending_payment/cancelled） — verified by BDD test
- [ ] B69: MODIFIED 申请退款仅限未发货订单 / 申请他人订单退款 — verified by BDD test
- [ ] B70: MODIFIED 审批通过退还积分并回补库存 / 审批通过（金额=实付、退券副作用） — verified by BDD test
- [ ] B71: MODIFIED 审批通过退还积分并回补库存 / 重复或并发审批 — verified by BDD test
- [ ] B72: MODIFIED 审批通过退还积分并回补库存 / 无审批权限 — verified by BDD test

### 继承自历史 contract 的 B*（refund ADDED，不在本轮 B1-B72；与本 feature 改动无冲突）
- [ ] B73: 驳回仅回退状态 / 驳回成功 — verified by BDD test
- [ ] B74: 驳回仅回退状态 / 缺少拒绝原因 — verified by BDD test
- [ ] B75: 驳回仅回退状态 / 重复驳回 — verified by BDD test
- [ ] B76: 驳回仅回退状态 / 驳回后再次申请 — verified by BDD test
- [ ] B77: 退款列表查看 / 查看列表 — verified by BDD test
- [ ] B78: 退款列表查看 / 无查看权限 — verified by BDD test

## Quality

- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 并发不变量——并发领取不超发（B14）、并发双击只一券（B17）、并发双击只一单（B47）、并发与重复支付只成交一次（B27）、超时取消与支付竞速只有一方生效（B32）、同一订单重复处理只释放一次（B33）
- [ ] Q3: 事务原子性——下单失败整体回滚：订单、库存两列、券锁、购物车均不变化（覆盖 B41/B42/B43）
- [ ] Q4: 幂等安全——领取同键重放不重发（B16）、下单同键同内容重放不新建（B44）、同键异内容 409（B45）、支付成功后再次支付 409（B27）、审批并发不重复退（B71）
- [ ] Q5: 越权 404——操作他人订单/地址/购物车行/优惠券一律 404（B43/B51/B28/B61/B69）；买家无主动取消端点已显式说明（order-payment Coverage Gaps）
- [ ] Q6: 状态机覆盖——订单 7 态枚举在 checkout/fulfillment/refund/order-payment 四份 spec 间一致；券 4 态机在 coupon 内自洽，跨 spec 引用为唯一出处
- [ ] Q7: openapi 契约门禁——`docs/api/openapi.yaml` 缺失的债务本 feature 不新增（沿用 follow-ups 列表）

## Schema Changes

### coupon_templates（新增）
- `id` UUID NOT NULL — PK
- `name` VARCHAR(64) NOT NULL
- `threshold_points` BIGINT NOT NULL CHECK (`threshold_points` > 0)
- `discount_points` BIGINT NOT NULL CHECK (`discount_points` > 0 AND `discount_points` < `threshold_points`)
- `total_count` INT NOT NULL CHECK (`total_count` > 0)
- `per_user_limit` INT NOT NULL CHECK (`per_user_limit` > 0)
- `received_count` INT NOT NULL DEFAULT 0 CHECK (`received_count` >= 0 AND `received_count` <= `total_count`)
- `valid_from` TIMESTAMPTZ NOT NULL
- `valid_until` TIMESTAMPTZ NOT NULL CHECK (`valid_until` > `valid_from`)
- `status` VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (`status` IN ('active','halted'))
- `created_at` / `updated_at` TIMESTAMPTZ

### user_coupons（新增）
- `id` UUID NOT NULL — PK
- `user_id` UUID NOT NULL — FK `users`(id) ON DELETE CASCADE
- `template_id` UUID NOT NULL — FK `coupon_templates`(id) ON DELETE RESTRICT
- `status` VARCHAR(16) NOT NULL CHECK (`status` IN ('available','locked','used','expired'))
- `order_id` UUID — FK `orders`(id), NULL allowed
- `request_id` VARCHAR(64) — NULL allowed
- `created_at` / `updated_at` TIMESTAMPTZ
- 部分唯一索引 `ON (order_id) WHERE status IN ('locked','used')` — 一张券同一时刻至多被一笔在途或已成交订单占用
- 部分唯一索引 `ON (user_id, request_id) WHERE request_id IS NOT NULL` — 领取幂等域
- 索引 `ON (user_id, status, id DESC)` — 我的券、结算页券列表

### products（变更）
- 新增 `locked_stock` INT NOT NULL DEFAULT 0 CHECK (`locked_stock` >= 0)
- `stock` 含义收窄为"可售、不含预占"

### orders（变更）
- `status` 枚举扩为 `pending_payment | paid | shipped | completed | refund_requested | refunded | cancelled`
- 新增 `pay_expire_at` TIMESTAMPTZ NOT NULL
- 新增 `discount_points` BIGINT NOT NULL DEFAULT 0 CHECK (`discount_points` >= 0)
- 新增 `pay_points` BIGINT NOT NULL CHECK (`pay_points` = `total_points` - `discount_points` AND `pay_points` > 0)
- 新增 `cancelled_at` TIMESTAMPTZ — NULL allowed
- `paid_at` TIMESTAMPTZ 改可空（仅 pending_payment→paid 时写入）
- 状态-时间戳一致性 CHECK 覆盖 7 态；存量已支付订单回填 `pay_expire_at = paid_at`
- 新增部分索引 `ON (pay_expire_at, id) WHERE status = 'pending_payment'` — 超时扫描

### permissions（种子数据新增）
- `coupon:read` — 模板列表（管理员）
- `coupon:write` — 模板创建、停发
- 授予超管、运营两角色
- 审计动作新增：券模板创建、券模板发放状态切换

## Follow-ups

- FU-6d2a90f3: `request_hash` 中 `coupon_id` 的规范化编码（未选券与选券两种输入的字节级编码）发布前冻结，沿用主档"编码冻结"条款。
- FU-4f9c21ab: 生命周期任务每轮取批的批量大小（超时待支付订单与过期可用券各取多少行）定默认值时与 `LIFECYCLE_SCAN_INTERVAL_SECONDS` 一并冻结。
- FU-3a07b15（本 feature 引入）: 限领计数 used/expired 状态券是否占额度未定义——即"用券成交后能否再领"的产品口径待定，影响 B13 与限领事务内 count 实现的精确边界。
- FU-4d12b0e8（本 feature 引入）: 模板列表"已核销数"在大总量下是否改物化计数未定义。
- FU-2e91a76c: 支付时限默认值 15 分钟的覆盖范围明确到业务 SLA 层（评测 W1 已落 spec，但若未来需对外公示需补产品口径文件）。
- FU-0a8e7f3b: 文档契约 `docs/api/openapi.yaml` 缺失的债务继承自仓库历史，本 feature 不新增，沿用历史 follow-ups。

## Pass Rule

ALL B1-B78 断言全过 + ALL Q1-Q7 + 后端 `go test ./...` + miniapp `pnpm test` + admin Playwright e2e + lint 全绿。
