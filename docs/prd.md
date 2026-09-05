# Shop-Mall PRD v1

> **术语映射**：本文采用 `docs/CONTEXT.md` 的口径（`hold` / `hold_stock` / `lock`）。如出现近义口语（待发货 / 预占 / 锁定），按该文件做语义映射。
>
> **数值引用**：本文 AC 中凡涉及阈值 / 时限 / 上限均不重复数字，统一引用附录 A「常量与阈值」中的配置项原名。

---

## 1. Introduction / Overview

shop-mall 是一个积分商城教学项目，覆盖买家小程序、管理后台、Go 后端、PostgreSQL 的完整链路。本 PRD 描述当前系统**已实现**的能力，目标是给后续改动提供可被 AI agent 直接执行的实现契约。

读者画像：AI agent（自动生成代码与测试）、工程师（实现 / 评审 / 回归）、讲师（在课堂上读摘要理解系统全貌）。每 capability 顶部以注释块形式给出教学语境，正文以 US + FR 形式给出可验证约束。

## 2. Goals

- **G-1** 描述当前已实现的全部买家端、管理端、领域能力，US 粒度到「一次实现 / 一次 PR 可独立交付」。
- **G-2** 每个 US 配可验证 Acceptance Criteria，覆盖功能正确性、并发、幂等、权限、安全红线。
- **G-3** 配套 FR 编号清单与常量附录，让 AI agent 与工程师按图施工，避免数字散落。
- **G-4** 显式记录历史「明确不做」翻案与配置项变更，给后人留反面教材与变更痕迹。

## 3. User Stories

### 3.1 买家端（小程序）

#### 3.1.1 登录与账号

> **教学语境 / 决策背景**：买家身份用微信 openid 唯一识别；不开放手机号 / 验证码登录，避免引入第二个身份源导致对账复杂度爆炸。开发环境走游客 AppID 模拟登录，让不连真机的开发也能跑通。

**US-101 免登录浏览**
- **Description**：作为未登录买家，我想浏览商品列表 / 分类 / 搜索 / 详情，不想被强制登录打扰。
- **Acceptance Criteria**：
  - [ ] 未登录状态下 `/pages/goods/list`、`/pages/goods/category`、`/pages/goods/search`、`/pages/goods/detail` 四个页面全部可打开
  - [ ] 列表 / 分类 / 搜索支持分页（每页 10 条）
  - [ ] 搜索为空时显示空结果提示
  - [ ] 点击「加购」或「去结算」触发登录拦截（跳转 `/pages/auth/login`）

**US-102 微信一键登录**
- **Description**：作为买家，我想通过微信 `wx.login` 一键登录，避免输入密码。
- **Acceptance Criteria**：
  - [ ] `wx.login` 取 `code` 后端以「一次一用」处理（防重放）
  - [ ] 登录成功签发 24h JWT（参见 `appendix.constants#AUTH_BUYER_JWT_TTL`）
  - [ ] token 过期 / 401 后清本地会话，下次写操作弹窗引导重登
  - [ ] 同 IP 登录频次超过 `appendix.constants#AUTH_BUYER_LOGIN_RATE_LIMIT` 被拒（429）

**US-103 首次登录赠送积分**
- **Description**：作为新买家，我首次登录想拿到一笔积分奖励，能直接用于兑换。
- **Acceptance Criteria**：
  - [ ] 金额由 `appendix.constants#SIGNUP_BONUS_POINTS` 决定（默认 100）
  - [ ] 同一 openid 重复登录不重复到账（DB 唯一索引兜底）
  - [ ] 积分明细页出现一条 `signup_bonus` 类型流水
  - [ ] 流水 `event_key` 含唯一键防并发场景重复记账

**US-104 个人资料**
- **Description**：作为买家，我想修改昵称与头像。
- **Acceptance Criteria**：
  - [ ] 昵称通过微信昵称键盘填写；保存后全局生效
  - [ ] 头像通过 `chooseAvatar` 取图后上传；服务端返回可访问地址
  - [ ] 头像图片存服务端文件卷，不落库二进制

#### 3.1.2 商品浏览

> **教学语境 / 决策背景**：商品浏览链路是后端「读路径」的代表，演示无状态查询 + 缓存接入点（虽然当前未启用缓存）。所有列表接口一律支持 cursor 或 offset 分页（当前 offset），为以后切 cursor 留口子。

**US-201 首页**
- **Description**：作为买家，我想在首页快速找到搜索入口、积分余额、分类入口与推荐商品。
- **Acceptance Criteria**：
  - [ ] 只展示 `status = on_sale` 的商品
  - [ ] 积分余额 banner 取自当前 buyer 的 `points_balance`
  - [ ] 分类快捷入口点击后跳到分类页并定位该分类
  - [ ] 推荐商品双列网格；下拉刷新拉取最新

**US-202 分类页**
- **Description**：作为买家，我想左右分栏浏览分类与商品。
- **Acceptance Criteria**：
  - [ ] 左侧分类栏来自服务端
  - [ ] 右侧商品网格滚动到底分页加载（每页 10 条）
  - [ ] 翻页不重复不遗漏（用 `(category_id, id)` 排序）

**US-203 搜索**
- **Description**：作为买家，我想按商品名搜索商品。
- **Acceptance Criteria**：
  - [ ] 大小写不敏感子串匹配
  - [ ] 单次最多返回 `appendix.constants#CATALOG_SEARCH_MAX_RESULT` 条
  - [ ] 空结果显示「无匹配商品」

**US-204 商品详情**
- **Description**：作为买家，我想看商品图集、价格、库存、描述。
- **Acceptance Criteria**：
  - [ ] 详情免登录可看
  - [ ] 图集轮播第一张为主图（来自 `images[0]`）
  - [ ] 当 `hold_stock + sold >= stock` 时显示「缺货」并禁用「加购」
  - [ ] 下架商品对买家不可见（直接返回 404 给详情接口）

#### 3.1.3 购物车

> **教学语境 / 决策背景**：购物车是下单前置环节；为了演示「幂等合并」与「乐观锁」，加购接口对同 `(buyer_id, product_id)` 自动 merge 数量。

**US-301 加购与合并**
- **Description**：作为买家，我想把商品加入购物车，重复加同一商品数量自动合并。
- **Acceptance Criteria**：
  - [ ] 加购需登录态
  - [ ] 同 `(buyer_id, product_id)` 已存在购物车行时数量相加
  - [ ] 不存在时新建一行
  - [ ] 行为契约见 `specs/cart/spec.md`

**US-302 行内管理**
- **Description**：作为买家，我想改数量或删除购物车行。
- **Acceptance Criteria**：
  - [ ] 改数量走 PATCH / 删除走 DELETE；即时同步服务端
  - [ ] 刷新小程序后状态与服务端一致
  - [ ] 数量上限校验在服务端（不接受客户端篡改）

**US-303 不可购标记**
- **Description**：作为买家，购物车里下架或缺货的行应被标记，不能去结算。
- **Acceptance Criteria**：
  - [ ] 商品下架 / 可售数为 0 时该购物车行标记 `purchasable = false`
  - [ ] 存在不可购行时「去结算」被拦截并提示
  - [ ] 后端结算接口对不可购行直接返回 422，不进入下单流程

#### 3.1.4 下单与支付（含超时取消）

> **教学语境 / 决策背景**：下单 + 支付是整个项目最复杂的 capability，演示「事务双写」「hold 与核销分离」「幂等键」「并发去重」「后台超时扫描」五种机制。US 拆细到每个机制一条，便于单测覆盖与排错定位。

**US-401-a 计算实付积分**
- **Description**：作为买家，结算页我想看到准确的实付积分（原价 - 券抵扣），价格不能由客户端算。
- **Acceptance Criteria**：
  - [ ] 服务端按当前商品积分价 × 数量 − 优惠券抵扣 计算 `pay_points`
  - [ ] 客户端传的 `pay_points` 一律忽略（不被采信）
  - [ ] 若选了过期 / 已用 / 非本人券，返回 422 并指明券 ID
  - [ ] 门槛不足的券不可用，返回 422

**US-401-b 预占库存与券**
- **Description**：作为买家，我想下单时库存与券被预占，避免超卖与券被他人抢用。
- **Acceptance Criteria**：
  - [ ] 同事务内：`products.hold_stock += 数量`，`coupons.status = hold`（若引入此中间态）或加 hold 记录
  - [ ] 预占动作有锁顺序保护（见 `docs/CONTEXT.md` lock 与架构 `02-backend.md`）
  - [ ] 任一商品可售数不足返回 422 并指出 product_id
  - [ ] 不写积分流水（流水留给支付）

**US-401-c 建订单 + 清购物车行**
- **Description**：作为买家，我想下单成功后订单生成且购物车对应行被清。
- **Acceptance Criteria**：
  - [ ] 同事务内：插入 `orders`（含商品快照 + 收货快照 + 实付积分 + 券 ID）
  - [ ] 订单状态为 `pending_payment`
  - [ ] 删除已提交购物车行
  - [ ] 任一步失败则整单回滚（含 hold 与券的预占）

**US-401-d 下单幂等**
- **Description**：作为买家，我想超时重试能拿到原订单，不想因为网络抖动产生重复订单。
- **Acceptance Criteria**：
  - [ ] 请求带 `Idempotency-Key` 头
  - [ ] 同键同请求重放：返回首次结果（订单 ID 不变）
  - [ ] 同键不同请求：返回 409
  - [ ] 并发双击同一键：只生成一单（由 DB 唯一约束兜底）
  - [ ] 行为契约见 `specs/checkout/spec.md`

**US-403-a 余额不足**
- **Description**：作为买家，支付时若余额小于实付积分，我想看到明确错误而不是扣成负数。
- **Acceptance Criteria**：
  - [ ] 返回 5xxx 段错误码（如 `INSUFFICIENT_BALANCE`）
  - [ ] 订单保持 `pending_payment`
  - [ ] 库存 hold 与券 hold 不变
  - [ ] 管理员补分后同一订单可再次支付成功

**US-403-b 并发支付只成交一次**
- **Description**：作为买家或攻击者，并发双击支付只应成交一单。
- **Acceptance Criteria**：
  - [ ] 同事务内：`SELECT ... FOR UPDATE` 锁订单行
  - [ ] 已为 `paid` 时第二次支付返回 409
  - [ ] 只扣一次积分 / 只写一条 `order_pay` 流水 / 只核销一次券
  - [ ] 有并发集成测试覆盖（`backend/tests/integration/order_pay_concurrency_test.go`）

**US-403-c 支付他人订单 403**
- **Description**：作为买家，我不应能支付别人的订单。
- **Acceptance Criteria**：
  - [ ] orderId 不属于当前 buyer 时返回 403
  - [ ] 不返回订单详情以防 ID 探测

**US-403-d 非 pending_payment 支付 409**
- **Description**：作为买家，已支付 / 已取消 / 已发货 / 已完成 / 退款中的订单不应能再次支付。
- **Acceptance Criteria**：
  - [ ] 订单状态不为 `pending_payment` 时支付返回 409
  - [ ] 响应 body 包含当前状态便于前端提示

**US-408 后台超时取消**
- **Description**：作为系统行为，`pending_payment` 订单超过支付时限应自动取消并释放 hold。
- **Acceptance Criteria**：
  - [ ] 后台 ticker 周期扫描（参见 `appendix.constants#ORDER_PAY_TIMEOUT`）
  - [ ] 超时订单状态置为 `cancelled` 并记 `cancelled_at`
  - [ ] 同事务内：`products.hold_stock -= 数量`，券从 `hold` 恢复为 `unused`
  - [ ] `cancelled` 订单不可发起退款（前端隐藏入口，后端 409）
  - [ ] 后台任务退出可停止（ticker + ctx）
  - [ ] 行为契约见 `specs/order-payment/spec.md`

#### 3.1.5 订单

> **教学语境 / 决策背景**：订单列表与详情是「读路径」，演示「按 buyer_id 过滤 + 状态机过滤」的标准模式。快照字段（商品 / 收货）演示 append-only 数据保护。

**US-404 订单列表**
- **Description**：作为买家，我想按状态过滤订单。
- **Acceptance Criteria**：
  - [ ] tab 与状态一一对应：全部 / 待支付 / 待发货 / 已发货 / 已完成 / 退款申请中 / 已退款 / 已取消
  - [ ] 倒序分页，每页 10 条
  - [ ] `cancelled` 订单在「已取消」tab 可见
  - [ ] 状态枚举与 `docs/CONTEXT.md §1.2` 一致

**US-405 订单详情**
- **Description**：作为买家，我想看订单全貌（含快照与时间节点）。
- **Acceptance Criteria**：
  - [ ] 展示：订单号 / 状态 / 商品快照 / 收货快照 / 各节点时间 / 实付积分 / 券抵扣明细 / 退款原因与拒绝原因
  - [ ] 商品 / 券模板 / 地址后续变更不影响历史订单字段

**US-406 确认收货**
- **Description**：作为买家，仅 `shipped` 订单可确认收货。
- **Acceptance Criteria**：
  - [ ] 仅 `shipped` 订单展示「确认收货」按钮
  - [ ] 确认后状态变 `completed` 并记 `completed_at`
  - [ ] 重复确认返回 409

**US-407 申请退款**
- **Description**：作为买家，我对 `paid` 订单可申请退款。
- **Acceptance Criteria**：
  - [ ] 退款原因必选（不想要了 / 拍错重下 / 其他，支持自定义补充）
  - [ ] 申请后订单进入 `refund_requested`
  - [ ] 驳回后可见拒绝原因并可再次申请
  - [ ] 仅 `paid` 订单在小程序展示入口

**US-407-a 后端仅 paid 守卫**
- **Description**：作为后端守卫，我对 `shipped` 及之后状态拒绝退款申请。
- **Acceptance Criteria**：
  - [ ] `shipped` / `completed` / `refund_requested` / `refunded` / `cancelled` 状态申请退款返回 409
  - [ ] 响应 body 包含当前状态
  - [ ] 行为契约见 `specs/refund/spec.md`

#### 3.1.6 优惠券

> **教学语境 / 决策背景**：券是相对新的能力（v0.3 引入），演示「模板与实例分离」「hold/核销/作废三态生命周期」。

**US-501 优惠券中心**
- **Description**：作为买家，我想浏览可领取的券模板并领取。
- **Acceptance Criteria**：
  - [ ] 列出可领取模板（`status = active` 且在 `valid_from / valid_until` 内）
  - [ ] 同一模板超「每人限领」不可再领
  - [ ] 模板停发后入口对买家不可见

**US-502 券使用与查看**
- **Description**：作为买家，结算页我想选券；「我的券」页我想看已领券状态。
- **Acceptance Criteria**：
  - [ ] 结算页可选一张在有效期内的 `unused` 券
  - [ ] 「我的券」页按状态分组：未使用 / 已使用 / 已过期 / 已作废
  - [ ] 取消订单 / 退款成功后券状态实时反映

#### 3.1.7 积分

> **教学语境 / 决策背景**：积分明细页是「对账入口」，所有余额变动都应有对应流水，流水累计等于余额（一致性恒等式）。

**US-601 积分明细页**
- **Description**：作为买家，我想看当前余额与全部流水。
- **Acceptance Criteria**：
  - [ ] 类型图标区分 `signup_bonus` / `order_pay` / `refund` / `admin_adjust` / `coupon`
  - [ ] 收入支出颜色区分
  - [ ] 流水累计恒等于当前余额（不一致时前端展示异常标记）

### 3.2 管理后台

#### 3.2.1 管理员登录

> **教学语境 / 决策背景**：管理端认证用 Cookie JWT（与买家 JWT 隔离），演示「双 Cookie / 双提交 CSRF / Origin 白名单」三条安全红线。

**US-701 管理员登录**
- **Description**：作为管理员，我想用用户名 + 密码登录后台。
- **Acceptance Criteria**：
  - [ ] 密码至少 12 位含大小写数字符号（参见 `appendix.constants#ADMIN_PASSWORD_MIN_LEN`）
  - [ ] 签发 HttpOnly Secure SameSite=Strict Cookie（参见 `appendix.constants#ADMIN_COOKIE_TTL`）
  - [ ] 写请求要求双提交 CSRF 头
  - [ ] Origin 走白名单
  - [ ] 改密 / 登出 / 停用使旧 token 立即失效（token 含版本号校验）
  - [ ] 行为契约见 `specs/admin-authz/spec.md`

#### 3.2.2 商品管理

**US-702 商品管理**
- **Description**：作为运营，我想列表筛选 / 新建 / 编辑 / 上下架商品。
- **Acceptance Criteria**：
  - [ ] 列表按状态筛选（在售 / 下架 / 全部）
  - [ ] 必填项：名称 / 描述 / 分类 / 积分价 / 库存 / 状态
  - [ ] 上下架即时生效（前端刷新可见）
  - [ ] 下架商品在买家端立即不可见，历史订单快照不受影响

#### 3.2.3 商品图管理

**US-703 商品图管理**
- **Description**：作为运营，我想上传商品图并管理主图。
- **Acceptance Criteria**：
  - [ ] 单商品最多 9 张图（参见 `appendix.constants#PRODUCT_IMAGE_MAX_COUNT`）
  - [ ] 仅接受 JPEG / PNG / WebP
  - [ ] 超 `appendix.constants#PRODUCT_IMAGE_MAX_SIZE` 自动压缩后上传
  - [ ] 第一张为主图（小程序详情轮播排第一）
  - [ ] 图片支持上下架

#### 3.2.4 订单管理与发货

**US-704 订单管理与发货**
- **Description**：作为运营，我想看订单列表与详情，并对 `paid` 订单发货。
- **Acceptance Criteria**：
  - [ ] 订单列表按状态筛选（含 `pending_payment` / `cancelled`）
  - [ ] 详情抽屉展示快照与时间节点
  - [ ] 「发货」仅对 `paid` 订单可用
  - [ ] 发货只记录发货人与时间，无物流单号
  - [ ] 对非 `paid` 订单发货返回 409
  - [ ] 发货动作写入审计日志

#### 3.2.5 退款审批

> **教学语境 / 决策背景**：退款审批演示「事务内多副作用」（退积分 + 回库存 + 券作废 + 状态变更）与「驳回不产生副作用」两个反面。

**US-705-a 通过：退积分 + 回库存 + 券作废**
- **Description**：作为超管，我想批准退款请求并一次性回退所有副作用。
- **Acceptance Criteria**：
  - [ ] 同一事务内：买家余额 += `pay_points`；`products.hold_stock -= 数量`；券置 `voided`；订单状态置 `refunded`
  - [ ] 写一条 `refund` 类型流水
  - [ ] 写审计日志
  - [ ] 任一步失败整事务回滚

**US-705-b 驳回：必填原因 + 回原状态**
- **Description**：作为超管，我想驳回退款请求并说明原因。
- **Acceptance Criteria**：
  - [ ] 拒绝原因必填（前端 + 服务端校验）
  - [ ] 订单状态回 `paid`
  - [ ] 不产生任何积分 / 库存 / 券副作用
  - [ ] 写审计日志

**US-705-c 运营角色无审批入口 403**
- **Description**：作为运营角色，我想看到我没有权限的入口不会被调通。
- **Acceptance Criteria**：
  - [ ] 退款列表对运营隐藏审批按钮
  - [ ] 直接调审批接口返回 403
  - [ ] 写一条失败的审计记录

#### 3.2.6 积分调整

**US-706 积分调整**
- **Description**：作为超管，我想按买家 ID 调整积分并在同页核对流水。
- **Acceptance Criteria**：
  - [ ] 增减带符号，备注必填
  - [ ] 每次提交生成新的 `Idempotency-Key`
  - [ ] 扣减不能把余额扣成负数（DB CHECK + 应用层校验双兜底）
  - [ ] 同键重放不重复到账；同键不同内容返回 409
  - [ ] 同页可拉取该买家近 N 条流水对账

#### 3.2.7 券模板管理

> **教学语境 / 决策背景**：券模板创建后规则字段冻结，防止历史已领取券的抵扣被追溯改变。

**US-707-a 创建：参数校验**
- **Description**：作为超管，我想创建券模板。
- **Acceptance Criteria**：
  - [ ] 必填：名称 / 门槛积分 / 抵扣积分 / 总量 / 每人限领 / `valid_from` / `valid_until`
  - [ ] 规则校验：门槛与抵扣均为正整数；抵扣 < 门槛；总量与限领均为正整数；`valid_until` 严格晚于 `valid_from`
  - [ ] 校验失败返回 1xxx 段错误码且不创建模板
  - [ ] 创建后初始状态 `active`，已领取数 0
  - [ ] 写成功审计

**US-707-b 创建后规则冻结**
- **Description**：作为超管，对已存在模板的规则字段修改应被忽略。
- **Acceptance Criteria**：
  - [ ] PATCH 请求中仅 `status`（active / halted）变更生效
  - [ ] 其它规则字段（门槛 / 抵扣 / 总量 / 限领 / 有效期）一律不被采信
  - [ ] 行为契约见 `specs/coupon/spec.md`

**US-707-c 停发只影响后续领取**
- **Description**：作为超管，我想停发模板但不影响已领券。
- **Acceptance Criteria**：
  - [ ] `active` ↔ `halted` 切换仅影响后续领取
  - [ ] 已领券仍按 `valid_until` 正常流转

#### 3.2.8 会员查询

**US-708 会员查询**
- **Description**：作为超管，我想按 ID 或昵称搜索买家。
- **Acceptance Criteria**：
  - [ ] 展示积分余额 / 注册时间
  - [ ] 「跳转积分调整」按钮直达 US-706
  - [ ] 运营角色无此菜单；直接调接口返回 403

#### 3.2.9 角色与管理员管理（只读版）

**US-709 角色与管理员管理（只读版）**
- **Description**：作为超管，我想看角色与管理员账号信息（写操作前端待接入）。
- **Acceptance Criteria**：
  - [ ] 展示角色与权限码映射
  - [ ] 展示管理员账号、角色、启用状态
  - [ ] 数据与 DB 一致
  - [ ] 未知权限码的接口调用返回 422
  - [ ] 写操作（建角色 / 改角色 / 停账号）后端接口已具备，前端待接入（见 §9 Open Questions）

#### 3.2.10 审计日志

**US-710 审计日志**
- **Description**：作为超管，我想查看操作日志。
- **Acceptance Criteria**：
  - [ ] 展示：操作人 / 角色 / 动作 / 对象 / 结果 / 时间
  - [ ] 成败颜色区分
  - [ ] 倒序分页
  - [ ] US-704 / US-705-a/b / US-706 / US-707-* 的每个动作都能查到，含失败尝试

## 4. Functional Requirements

> 本节为 FR 编号清单（与 US 一一对应或合并）。每条 FR 是对系统行为的不可变声明，供实现与契约测试对齐。

### 4.1 买家端

| FR | 说明 | 对应 US |
|---|---|---|
| FR-1 | 商品浏览链路对未登录买家开放 | US-101 |
| FR-2 | 微信 `code` 以「一次一用」处理；JWT TTL 走 `AUTH_BUYER_JWT_TTL` | US-102 |
| FR-3 | 同 openid 只能领取一次 `SIGNUP_BONUS_POINTS` | US-103 |
| FR-4 | 列表分页每页 10 条；搜索上限走 `CATALOG_SEARCH_MAX_RESULT` | US-201 / US-202 / US-203 |
| FR-5 | 详情页可售数为 0 时禁用加购 | US-204 |
| FR-6 | 加购对同 `(buyer_id, product_id)` 合并数量 | US-301 |
| FR-7 | 购物车行 PATCH / DELETE 即时同步服务端 | US-302 |
| FR-8 | 不可购行不可去结算 | US-303 |
| FR-9 | 下单实付积分由服务端按 当前积分价 × 数量 − 券抵扣 计算 | US-401-a |
| FR-10 | 同事务内 hold 库存与券 | US-401-b |
| FR-11 | 同事务内建订单 + 清购物车行 + 记录快照 | US-401-c |
| FR-12 | 下单接口走 `Idempotency-Key`，同键同请求重放、同键不同请求 409 | US-401-d |
| FR-13 | 余额不足返回 `INSUFFICIENT_BALANCE`，hold 不变 | US-403-a |
| FR-14 | 并发支付通过订单行 lock 去重 | US-403-b |
| FR-15 | 支付他人订单返回 403 | US-403-c |
| FR-16 | 非 `pending_payment` 支付返回 409 | US-403-d |
| FR-17 | 后台 ticker 周期扫描 `pending_payment` 订单，超 `ORDER_PAY_TIMEOUT` 置 `cancelled` 并释放 hold | US-408 |
| FR-18 | 订单列表 8 个状态 tab 与 `CONTEXT.md §1.2` 一致 | US-404 |
| FR-19 | 订单详情展示快照不被后续变更覆盖 | US-405 |
| FR-20 | 仅 `shipped` 订单可确认收货 | US-406 |
| FR-21 | 退款申请仅 `paid` 订单可发起，其它状态 409 | US-407 / US-407-a |
| FR-22 | 券模板 `active` 且在有效期内可领取，超「每人限领」拒绝 | US-501 |
| FR-23 | 取消订单 / 退款成功后券状态实时反映 | US-502 |
| FR-24 | 流水累计恒等于余额 | US-601 |

### 4.2 管理端

| FR | 说明 | 对应 US |
|---|---|---|
| FR-30 | 管理员密码至少 `ADMIN_PASSWORD_MIN_LEN` 位含大小写数字符号 | US-701 |
| FR-31 | 写请求要求双提交 CSRF + Origin 白名单 | US-701 |
| FR-32 | 改密 / 登出 / 停用使旧 token 立即失效（版本号校验） | US-701 |
| FR-33 | 商品新建必填字段；上下架即时生效 | US-702 |
| FR-34 | 单商品最多 `PRODUCT_IMAGE_MAX_COUNT` 张图；JPEG/PNG/WebP；超 `PRODUCT_IMAGE_MAX_SIZE` 自动压缩 | US-703 |
| FR-35 | 仅 `paid` 订单可发货；发货只记人与时间 | US-704 |
| FR-36 | 退款通过：同事务退积分 + 回库存 + 券作废 + 状态置 `refunded` | US-705-a |
| FR-37 | 退款驳回：原因必填，订单回 `paid`，无副作用 | US-705-b |
| FR-38 | 退款审批权限校验：运营角色 403 + 失败审计 | US-705-c |
| FR-39 | 积分调整带 `Idempotency-Key`；扣减不下溢负 | US-706 |
| FR-40 | 券模板创建参数校验；规则字段创建后冻结 | US-707-a / US-707-b |
| FR-41 | 停发只影响后续领取 | US-707-c |
| FR-42 | 会员查询接口运营角色 403 | US-708 |
| FR-43 | 角色 / 管理员写操作后端具备，前端待接入 | US-709 |
| FR-44 | 审计日志含失败尝试 | US-710 |

### 4.3 全局

| FR | 说明 |
|---|---|
| FR-50 | 一切数值用整数；超限 ID 用十进制字符串传输 |
| FR-51 | 余额与流水同一事务双写 |
| FR-52 | 写操作幂等（`Idempotency-Key`）：同键同请求重放、同键不同请求 409 |
| FR-53 | 锁顺序：订单 → 用户 → 地址 → 购物车 → 商品按 ID 升序 |
| FR-54 | 死锁 / 序列化失败整事务重试，单条 SQL 不重试 |
| FR-55 | 审计日志 DB 触发器禁止 UPDATE / DELETE |
| FR-56 | 服务端权限校验是唯一安全边界；前端菜单仅体验 |

## 5. Non-Goals（Out of Scope）

> 显式记录「明确不做」事项与历史翻案，给后人留反面教材。

### 5.1 当前明确不做

| 事项 | 为什么不做 | 边界怎么留 |
|---|---|---|
| 真实资金支付 / 退款打款 / 支付回调对账 | 资金业务复杂度与教学目标无关 | 「确认支付」只认「积分扣减 + 券核销」同一事务；接口形状留出支付网关可替换位置 |
| 营销活动叠加（满 N 件打折 / 跨店满减 / 积分抵现叠加券等） | 复杂度爆炸且不教学 | 券按单张抵扣；商品定价为单值 |
| 物流单号与轨迹 | 无真实发货 | 发货只记人与时间 |
| 订阅消息 / 自动确认收货 | 依赖真实支付场景才有意义 | 确认收货为买家手动动作 |
| 手机号 / 验证码登录 / 找回密码 | 买家只走微信；管理员靠超管重置 | — |
| 多商户 / 多店铺 | 单商户模型 | 商品表无商户维度 |

### 5.2 历史翻案记录

> 此表记录「明确不做」事项被翻案的过程。每条翻案需注明原决议日期、翻案日期、原因。

| 事项 | 原决议 | 翻案日期 | 翻案原因 | 当前边界 |
|---|---|---|---|---|
| 优惠券 / 满减等营销 | v0.2（2026-09-01）PRD §7 明确不做 | 2026-09-06（v0.3） | 教学场景需要演示「模板与实例分离」「hold / 核销 / 作废生命周期」 | 券按单张抵扣；不做叠加营销 |
| 支付（一次性扣减） | v0.2 PRD §3 状态机：下单即 `paid` | 2026-09-06（v0.3） | 真实支付场景天然有「下单未付 / 超时取消」中间态，统一建模便于演示后台任务 | 支付动作独立为 `POST /api/v1/orders/{id}/pay`；超时由后台任务置 `cancelled` |

## 6. Design Considerations

### 6.1 现有组件复用

- 后端分层：`backend/internal/{buyer-auth,admin-authz,catalog,cart,checkout,coupon,order-payment,fulfillment,refund,points}` 共 10 个 capability 包；本文 US 按包对齐
- 行为契约：`specs/<capability>/{spec.md,contract.md}` 是 US 的 truth source；US 描述与之对齐，冲突时以 spec 为准
- 管理后台路由：`admin/src/menu.ts` 是菜单与权限映射的单一来源；US-705-c / US-708 / US-709 的前端可达性以此为准
- 小程序端页面：买家端 US 涉及的小程序页面位于 `miniapp/src/pages/`，UI 验收以真机 / IDE 截图为准

### 6.2 数据模型

- 表结构：`docs/tech-specs/data-model.md`
- UUID 主键迁移：`docs/architecture/08-uuid-primary-keys.md` + evidence / follow-ups
- 券与支付生命周期：`docs/architecture/07-coupon-pay-lifecycle.md` + evidence / follow-ups

### 6.3 术语统一

- 所有 US / FR / AC 用 `docs/CONTEXT.md` 口径（`hold` / `hold_stock` / `lock`）
- 旧文档（`prd.md`）的「预占 / 锁定 / 锁定库存」按该文件做语义映射

## 7. Technical Considerations

### 7.1 性能与可观测

- N-004 不承诺具体 QPS；Prometheus 导出请求量与 P95 延迟直方图
- `/metrics` 仅内网访问，由 nginx 屏蔽公网
- 告警规则位于 `deploy/prometheus/alerts.yml`（共 7 条），含超时取消队列堆积告警
- 后台任务心跳纳入指标（v0.3 新增）

### 7.2 安全红线

- N-001 买家 JWT 与管理端 Cookie JWT 签发方 / 受众 / 密钥环 / 有效期强制不同，启动时配置校验
- N-002 手机号 / 收货地址 / 微信 code / 密钥不进日志；结构化日志全程携带 `trace_id`，错误响应回传 `trace_id`
- FR-32 改密 / 登出 / 停用使旧 token 立即失效（token 含版本号校验）
- FR-55 审计日志 DB 触发器禁止 UPDATE / DELETE

### 7.3 部署与备份

- N-006 Docker Compose 一键起 PG / app / nginx(HTTPS) / Prometheus / Alertmanager
- N-007 RPO ≤ 1h / RTO ≤ 4h；备份恢复走 `deploy/backup/drill.sh` 演练

### 7.4 交付门禁（N-008）

PR 触发 CI 四道检查：
1. OpenAPI 契约一致性（`make contract-check` → `tools/openapi/` 下的 generate.ts / redocly.yaml / test/）
2. 后端全量测试（`go test ./...`）
3. 前端构建 + 类型 + 单测（`make frontend-checks`）
4. 迁移与镜像构建（迁移只向前，goose 无 down）

### 7.5 已知约束

- 单一 PG 实例；测试与生产实例分开
- 后台任务单进程；超时扫描与图片清理同源（ticker + ctx）
- OpenAPI 源在 `tools/openapi/`，由 codegen 产出对外 spec；本文与 prd.md 描述一致

## 8. Success Metrics

> 不承诺具体 QPS 或转化率（教学项目单实例部署）。以下为可观测的「系统健康」指标：

- **SM-1** CI 四道闸门全绿（contract-check / go test / frontend / migration + image-build）
- **SM-2** 后端 26 包 + 后台任务配套测试全绿；并发集成测试覆盖：下单去重 / 支付去重 / 超时取消释放
- **SM-3** Lighthouse a11y 100（管理后台 6 个核心页面）
- **SM-4** 备份演练 `deploy/backup/drill.sh` RPO ≤ 1h / RTO ≤ 4h
- **SM-5** Prom alerts 7 条无长期 firing
- **SM-6** 权限矩阵 14 码均能在 `specs/admin-authz/spec.md` 找到契约

## 9. Open Questions

| # | 问题 | 影响 | 建议 |
|---|---|---|---|
| 1 | 角色 / 管理员写操作前端待接入（US-709） | 账号管理只能改库 | 二选一：补前端，或把 `ADMIN_TARGET.md` 验收口径改为明确裁剪 |
| 2 | 买家端「我的券」入口与「券使用」结算页联动尚未定稿 | 影响 US-502 验收 | 与小程序端对齐交互草案后再锁口径 |
| 3 | 5.2 历史翻案表是否要 reopen 早期已闭环的「明确不做」 | 历史可追溯 vs 表格膨胀 | 当前已记录两条（优惠券、支付）；后续翻案必须入表 |

---

## 附录 A：常量与阈值

> 任何 US / AC 涉及数值均引用本表配置项原名；不重复数字。

| 配置项 | 当前值 | 含义 | 引用 |
|---|---|---|---|
| `AUTH_BUYER_JWT_TTL` | 24h | 买家 JWT 有效期 | US-102 |
| `AUTH_BUYER_LOGIN_RATE_LIMIT` | 20 / 分钟 / IP | 买家登录频次上限 | US-102 |
| `SIGNUP_BONUS_POINTS` | 100 | 新买家首次登录赠送积分 | US-103 |
| `CATALOG_SEARCH_MAX_RESULT` | 50 | 单次搜索结果上限 | US-203 |
| `PRODUCT_IMAGE_MAX_COUNT` | 9 | 单商品图集张数上限 | US-703 |
| `PRODUCT_IMAGE_MAX_SIZE` | 2 MB | 单图大小上限（超则自动压缩） | US-703 |
| `ADMIN_PASSWORD_MIN_LEN` | 12 | 管理员密码最小长度 | US-701 |
| `ADMIN_COOKIE_TTL` | 30 分钟 | 管理端 Cookie 有效期 | US-701 |
| `ORDER_PAY_TIMEOUT` | 待定（参见 `specs/order-payment/spec.md`） | `pending_payment` 订单支付时限 | US-408 |
| `ORDER_PAGE_SIZE` | 10 | 订单列表每页条数 | US-404 / US-202 |
| `POINTS_LEDGER_VISIBLE_COUNT` | 待定 | 积分明细页默认展示条数 | US-601 / US-706 |

> **维护规则**：新增配置项需在 `docs/tech-specs/data-model.md` 与本表同步登记；数值调整需走 PR 并更新本表。

## 附录 B：变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v0.2 | 2026-09-01 | 按实现情况重写，补需求编号、验收标准、权限矩阵、待解决问题 |
| v0.3 | 2026-09-06 | 状态机扩为 7 状态；新增优惠券能力；第 7、9 节按代码对齐 |
