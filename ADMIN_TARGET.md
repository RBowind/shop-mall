# 管理后台目标文档

这份文档定义 shop-mall 管理后台（admin）「做完」的标准。任何 AI agent 接手本任务，都对着它收敛：功能清单逐条做完，验收标准逐条通过，才算完成。验证失败时，把失败点对照回本文档，继续修，直到全过。

## 技术基线

admin 目录用 Ant Design Pro V6 重建，废弃现有的零框架 esbuild 方案。

- 框架：Umi Max（Umi 4）
- UI：antd 5 + @ant-design/pro-components（ProLayout / ProTable / ProForm / ProDescriptions）
- 语言：React 18 + TypeScript
- 构建：Umi 内置（webpack / mako），删除现有的 `build:browser`（esbuild）脚本与相关文件
- 现有接口契约、双 JWT、CSRF、幂等键机制全部保留，不重写后端已有能力，只补欠账
- 页面划分沿用现状（登录 / 商品 / 订单 / 退款 / 积分 / 权限账号 / 改密），壳换 Pro，交互与数据流保持等价

## 范围边界

本次全做，分两阶段交付：

- 前端 Pro 重建：登录、商品、订单、退款、积分、权限账号、改密 7 页 + 新增会员管理、审计日志 2 页
- 后端补 3 个「已实现但未暴露」的写接口：`POST /api/admin/v1/roles`、`PATCH /api/admin/v1/roles/{roleId}`、`PATCH /api/admin/v1/admin-users/{adminUserId}`
- 后端新增 3 个读接口：会员列表、审计日志查询、积分流水查询（当前后端完全没有这三个）

长期不做（PRD 已明确 P2）：优惠券营销、真实支付、物流单号、订阅消息自动确认收货、验证码忘记密码。

保留边界：管理员账号「创建」不做接口，只走 `admin-bootstrap` 初始化，与现状一致。

## 交付节奏

- 阶段一（主流程）：登录、商品、订单
- 阶段二（补全）：退款、积分调整与流水、改密、权限账号、会员管理、审计日志

阶段一完成并验收通过，再进阶段二。

## 验收方式

每个验收项前标注验证手段：

- **[断言]**：可脚本或接口验证。后端接口真实存在并可调用，前端确实调用了它，数据闭环成立。
- **[人核]**：浏览器打开页面截图人核。页面由 antd 组件渲染、信息完整、能点、能存、报错有反馈。
- **[a11y]**：用无障碍审计工具验证，Lighthouse 无障碍得分 100，无孤儿输入框。

## 功能清单与验收标准

### 1. 全局壳与权限

Pro 布局壳，侧边菜单按权限渲染，头部显示当前管理员与角色，带退出登录、改密入口。

- [断言] 未登录访问任意受保护路由，重定向到 `/user/login`。→ `admin/e2e/shell.spec.ts#S1`
- [断言] 登录后 `/auth/me` 返回的角色权限决定左侧菜单项；无权限的菜单项不渲染，直接访问其 URL 返回无权限提示。→ `admin/e2e/shell.spec.ts#S2`
- [断言] 对每个写请求，`X-CSRF-Token` header 由 `csrf_token` cookie 自动注入（登录接口除外）。→ `admin/e2e/shell.spec.ts#S3`
- [断言] 会话过期或 401，统一跳回登录页并提示。→ `admin/e2e/shell.spec.ts#S4`
- [人核] 抽 3 个页面，侧边菜单、面包屑、标题、头部用户区均用 Pro 组件渲染，无裸 HTML 拼凑痕迹。

### 2. 登录 / 登出 / 改密

- [断言] 正确凭据登录成功，写入 HttpOnly session cookie 与 csrf_token；错误凭据返回 401 与中文错误提示。→ `admin/e2e/auth.spec.ts#A1`
- [断言] 用户名、密码有非空与格式校验（沿用后端 ValidateUsername / ValidatePassword：3-32 位，密码至少 12 位含大小写数字符号）。→ `admin/e2e/auth.spec.ts#A2`
- [断言] 登出后旧会话对 `/auth/me` 返回 401。→ `admin/e2e/auth.spec.ts#A3`
- [断言] 改密需校验旧密码；成功后旧会话失效，需重新登录。（切片二补 spec）
- [人核] 登录页、改密页用 ProForm 渲染，提交有 loading，失败有内联错误。

### 3. 商品管理

对应小程序商品实体。列表、筛选、新建、编辑、图片上传、状态上下架。

- [断言] `GET /api/admin/v1/products` 返回分页商品列表，含状态、价格、库存；前端列表真实渲染这些字段。→ `admin/e2e/products.spec.ts#P1`
- [断言] 状态筛选（全部 / 在售 / 下架）真正把 status 参数传给后端，结果与筛选一致。→ `admin/e2e/products.spec.ts#P2`
- [断言] 新建商品 POST 成功后，买家端 `GET /products` 能查到该商品（数据闭环）。→ `admin/e2e/products.spec.ts#P3`
- [断言] 编辑商品 PATCH 成功后，`GET /products/{id}` 返回新值。→ `admin/e2e/products.spec.ts#P4`
- [断言] 图片上传 POST `/api/admin/v1/images` 支持多图：商品图库最多 9 张、首张即主图，保存后买家端 `images` 数组与 `main_image`（=第一张）一致；超过 2MB 在浏览器端自动压缩后上传（不再硬拒绝），非 JPEG/PNG/WebP 被拒绝。→ `admin/e2e/products.spec.ts#P5`
- [断言] 下架商品后，买家端列表不再展示该商品；上架后恢复。→ `admin/e2e/products.spec.ts#P6`
- [人核] 商品列表用 ProTable 渲染，含分页、筛选；新建/编辑用 ProForm 弹窗，图片上传有预览与 loading 态。

### 4. 订单管理

对应小程序订单实体。列表、状态筛选、详情、发货。

- [断言] `GET /api/admin/v1/orders` 分页列表，状态筛选（5 种状态）传入后端并生效。→ `admin/e2e/orders.spec.ts#O1`
- [断言] 点击订单进详情，`GET /api/admin/v1/orders/{orderId}` 返回状态、收货人、地址、退款/拒绝原因、商品明细，前端逐项渲染。→ `admin/e2e/orders.spec.ts#O2`
- [断言] 对 paid 订单且有 `order:ship` 权限时，发货按钮可用；POST `/api/admin/v1/orders/{orderId}/ship` 成功后状态从 paid 变 shipped。→ `admin/e2e/orders.spec.ts#O3`
- [断言] 无 `order:ship` 权限时，发货按钮不渲染或禁用。→ `admin/e2e/orders.spec.ts#O4`
- [断言] 发货后买家端该订单状态变 shipped（数据闭环）。→ `admin/e2e/orders.spec.ts#O5`
- [人核] 订单列表用 ProTable；详情用 ProDescriptions 展示，状态标签有颜色区分。

### 5. 退款审核

对应小程序退款申请。列表、通过、驳回。

- [断言] `GET /api/admin/v1/refunds` 只返回 refund_requested 状态的订单。
- [断言] 通过：POST `/api/admin/v1/refunds/{orderId}/approve` 后订单变 refunded，买家积分退回（买家端 `GET /points/ledger` 能查到 order_refund 流水）。无 `refund:approve` 权限时不显示按钮。
- [断言] 驳回：理由必填；POST `/api/admin/v1/refunds/{orderId}/reject` 后订单状态带拒绝原因，买家端可见。
- [人核] 驳回用内联表单，理由为空时阻止提交并提示。

### 6. 积分调整与流水

对应小程序积分账本实体。调整积分、按用户查流水。

- [断言] 积分调整页必须改为「从会员列表选一个买家」，不再要求手工填 int64 用户 ID。
- [断言] POST `/api/admin/v1/points/adjust` 每次提交生成新 Idempotency-Key；重复提交返回幂等结果而非重复记账。
- [断言] 调整成功后展示 delta 与 balance_after，且买家端 `GET /points/ledger` 能查到 admin_adjust 流水。
- [断言] 新增 `GET /api/admin/v1/points/ledger?user_id=...`，返回该用户积分流水（order_pay / order_refund / signup_bonus / admin_adjust），前端积分流水页渲染。
- [人核] 积分调整用 ProForm；积分流水页用 ProTable 展示，含操作类型、delta、余额快照、时间。

### 7. 权限账号（角色 + 管理员账号）

对应管理员/角色/权限实体。当前只读，需补写操作。

- [断言] `GET /api/admin/v1/roles`、`GET /api/admin/v1/admin-users` 现有读接口正常，前端列表渲染。
- [断言] 补实现 `POST /api/admin/v1/roles`：新建角色并分配权限码，成功后出现在角色列表。
- [断言] 补实现 `PATCH /api/admin/v1/roles/{roleId}`：修改角色权限码，成功后角色详情反映变更。
- [断言] 补实现 `PATCH /api/admin/v1/admin-users/{adminUserId}`：启用 / 停用 / 换角色，成功后管理员列表状态与角色更新。
- [断言] 无 `role:manage` 权限时，以上写操作入口不渲染，直接调用返回 403。
- [人核] 权限页不再是「只读提示」；角色、管理员账号用 ProTable，编辑用 ProForm 弹窗，停用有二次确认。

### 8. 会员管理（新增）

对应买家用户实体。当前后端无任何会员接口，需新增。

- [断言] 新增 `GET /api/admin/v1/users`：分页返回买家（ID / 昵称 / 积分余额 / 注册时间）。
- [断言] 会员列表支持按 ID 或昵称搜索。
- [断言] 从会员列表选中一个买家，能跳转到积分调整页并自动带出该买家，解决「手工填 ID 无来源」的问题。
- [断言] 无 `user:read`（或等价）权限时列表接口返回 403。
- [人核] 会员管理页 ProTable 渲染，积分余额、注册时间列可见。

### 9. 审计日志（新增）

对应 audit_logs 表，当前只写不读，需补读接口与页面。

- [断言] 新增 `GET /api/admin/v1/audit-logs`：分页返回审计日志（操作人 / 动作 / 对象 / 时间 / 详情），按时间倒序。
- [断言] 商品、订单、退款、积分、权限等每个写操作都产生对应审计日志，页面能查到。
- [断言] 无 `audit:read`（或等价）权限时返回 403。
- [人核] 审计日志页 ProTable 渲染，含操作人、动作、时间、对象，支持分页。

## 全局验收

全部模块验收通过后，再过一遍：

- [断言] 后端 `go test ./...` 全绿（含新增接口的单测）。
- [断言] 前端 `pnpm build` 通过，无类型错误。
- [a11y] 登录、商品、订单、退款、积分、权限、会员、审计、改密 9 个页面，Lighthouse 无障碍得分 100，孤儿输入框为 0。
- [人核] 上述 9 页抽验：点击目标不小于 44px，状态有视觉区分，空列表有空状态提示，请求失败有错误反馈。