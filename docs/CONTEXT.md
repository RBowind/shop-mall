# Shop-Mall Glossary

> **唯一词汇表**。`docs/prd.md`（产品视角）与本仓库的 `specs/`、techspec（实现视角）的术语以本文为准。
> 旧文出现的近义口语（待发货 / 预占 / 锁定库存）保留自然语感，读者按本文做语义映射。裸「锁定」不在此列——它专指 1.1 的行级并发控制机制。
> 本文件**只放词汇**，不写 US / AC / FR / 实现细节。

## 1. 核心术语

### 1.1 库存与券的占用生命周期

| 术语 | 定义 | 与近义词的关系 |
|---|---|---|
| **hold（预占）** | 下单时对库存数量与券实例的占用动作；订单 `pending_payment` 时存在；支付核销 / 取消释放 / 退款按有效期处置三种终结路径 | 「预占」「hold」「下单锁定」语义相同，统一用 hold |
| **hold_stock（库存预占数）** | 商品表上的字段，记录当前被未支付订单 hold 的数量；`可售数 + hold_stock = 总库存` | 旧文「锁定数」「锁定库存」语义相同，统一用 hold_stock |
| **lock（锁定）** | 数据库行级并发控制，是机制而非业务动作；不暴露给业务层 API | 「行级锁」「数据库锁」语义相同，统一用 lock |

### 1.2 订单状态

| 术语 | 枚举值 | 触发动作 |
|---|---|---|
| `pending_payment` | 待支付 | 下单成功（hold 库存与券） |
| `paid` | 已支付 | 买家确认支付成功（扣积分、核销券、消耗 hold） |
| `cancelled` | 已取消 | 后台超时任务扫描后置入（释放 hold 与券占用） |
| `shipped` | 已发货 | 管理员发货 |
| `completed` | 已完成 | 买家确认收货 |
| `refund_requested` | 退款申请中 | 买家在 `paid` 订单发起退款 |
| `refunded` | 已退款 | 超管审批通过（退积分、回库存、券按有效期处置） |
| **成交（deal）** | — | 订单从 `pending_payment` 转为 `paid` 的那一刻；下单不称为成交，两者不可混用 |

### 1.3 角色与权限

| 术语 | 定义 |
|---|---|
| **Buyer（买家）** | 小程序侧用户；以微信 openid 为唯一身份 |
| **AdminUser（管理员）** | 管理后台侧用户；分运营（Operator）与超管（SuperAdmin） |
| **Permission（权限码）** | 形如 `<resource>:<action>` 的字符串；后端每次请求实时查库解析，不写进 token |
| **权限码总数** | 已实现 12 个；券域 2 个（`coupon:read`、`coupon:write`）已规格化未实现，声明合计 14 个 |

### 1.4 优惠券

| 术语 | 定义 |
|---|---|
| **CouponTemplate（券模板）** | 券的发行规则（门槛 / 抵扣 / 总量 / 每人限领 / 有效期）；超管创建，规则字段创建后冻结 |
| **Coupon（券实例）** | 买家领取后持有的券；状态四态：`available` / `held` / `used` / `expired` |
| **核销（redeem）** | 支付成功时把 `held` 券置为 `used` |
| **释放（release）** | 订单取消或退款审批通过时，把券按有效期置为 `available` 或 `expired` |
| **过期（expire）** | 可用券超过有效期，或退款审批通过时已过期，置为 `expired`；`expired` 不再复用 |

### 1.5 积分

| 术语 | 定义 |
|---|---|
| **points_balance（积分余额）** | 买家表的字段；恒 ≥ 0，由数据库 CHECK 约束兜底 |
| **LedgerEntry（流水行）** | `points_ledger` 表的一行；append-only；类型为 `signup_bonus` / `order_pay` / `order_refund` / `admin_adjust` |

### 1.6 审计

| 术语 | 定义 |
|---|---|
| **AuditLog（审计日志）** | `audit_logs` 表的一行；append-only；DB 触发器禁止 UPDATE / DELETE；记录操作人 / 动作 / 对象 / 结果 / trace_id |

### 1.7 门禁与合并

| 术语 | 定义 |
|---|---|
| **质量门（gate）** | 挂进流水线、机器判定的确定性检查；只有质量门有 required check 资格。AI 初审意见是 advisory（参考），不进门 |
| **合门前检查（pre-merge checks）** | 每个 PR 必跑的检查集合，全绿才允许合并；红灯锁死 = required check 失败时 GitHub 拒绝合并的状态 |
| **合门后体检（post-merge checks）** | push 到 main 后跑的检查 + 后端镜像构建；产出发布产物，不承担挡合并的职责 |
| **改动行覆盖率（diff coverage）** | 只统计本次 PR 改动行的覆盖率口径；刻意区别于全仓总覆盖率，防凑数测试刷总量 |
| **变异效力（test efficacy）** | 变异测试工具判定的测试套件杀伤率，衡量"测试是不是假的"；当前为夜跑报告，非合并门 |
| **演示分支（demo branch）** | 仓库常驻的教学分支（`broken-test`、`untested-change`），专用于演示红灯锁死；PR 开而不合 |

_Avoid_: 用"覆盖率"裸指总量口径（门禁语境一律先问是不是改动行口径）；用"检查"混指 advisory 与门。

## 2. 引用约定

- 旧文档（`prd.md`）的「待发货」对应 `paid`；「已发货」对应 `shipped`；「待支付」对应 `pending_payment`；「已取消」对应 `cancelled`。
- US / AC 描述里凡出现数值（时限 / 上限 / 阈值），引用 `docs/prd.md` 附录 A 的配置项原名，不在文档体里复述数字。
- 本表新增术语需在 `docs/decisions/decision-log.md` 留一条决策记录，否则视同口径漂移。
