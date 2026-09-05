# shop-mall 系统 Tech Spec Evidence

- GF-4c9e2b7a：`products.main_image` 单图列已被 forward migration 替换为 `images JSONB` 有序图集（首图即主图），回填旧值后删除旧列。来源：已核实 `backend/migrations/0004_product_images.up.sql`。
- GF-8d1f5a3c：商品 API 视图保留 `main_image`，为图集首图派生的公共 URL，兼容旧客户端，非存储列。来源：已核实 `docs/api/openapi.yaml`（Product schema required 与 main_image 字段描述）。
- GF-b6e0c48d：`products.category VARCHAR(32) NOT NULL DEFAULT ''` 由 migration 引入；分类目录是代码静态枚举（digital、home、beauty、food、apparel，含中文名与展示顺序），`GET /categories` 与在售实时计数配对；编辑商品时未知目录键 422。来源：已核实 `backend/migrations/0005_product_category.up.sql`、`backend/internal/product/category.go`（CategoryCatalog）、`docs/api/openapi.yaml`（listProducts 的 category 参数；ProductWriteRequest.category 描述与 adminCreateProduct 的 422 响应）。
- GF-2a7d9f5e：权限码在 0002 种子 10 项之外增补 `user:read` 与 `audit:read`，仅授予超管。来源：已核实 `backend/migrations/0003_add_admin_read_permissions.up.sql`。
- GF-9f3b1c6d：`GET /api/admin/v1/users`（`user:read`，keyword 数字匹配买家 ID、否则匹配昵称）与 `GET /api/admin/v1/audit-logs`（`audit:read`）已在后端路由注册。来源：已核实 `backend/internal/admin/handler.go`（ListUsers、ListAuditLogs 路由）。
- GF-e5c8a2b4：首个收货地址不自动设为默认，买家需要时主动设默认。来源：用户确认，记录于 `docs/architecture/06-address-miniapp.md`（背景与目标·产品决策）。
- GF-c2f84a1d：积分事件键实际格式为 `signup:{user_id}`、`order_pay:{order_id}`、`order_refund:{order_id}`、`admin_adjust:{admin_id}:{idempotency_key}`，与 05 文档旧示例不同；用户裁决以代码为准，库中存量流水即此格式，`docs/architecture/` 02/05/00 已同步修正。来源：已核实 `backend/internal/payment/ledger.go`（OrderPayEventKey、OrderRefundEventKey、AdminAdjustEventKey）、`backend/internal/user/service.go`（awardSignupBonus）；用户确认。
- GF-6b0d3e9f：下单幂等为前置重放检查（事务内按 `(user_id, client_token)` SELECT FOR UPDATE）+ 普通 INSERT + 唯一约束冲突回滚后事务外重读判定，锁序订单行→用户→地址/购物车→商品升序；用户裁决以代码为准，`docs/architecture/` 02/05/00 相应段落已同步修正，不采用 ON CONFLICT DO NOTHING RETURNING 单语句方案。来源：已核实 `backend/internal/application/order/create.go`（Execute、lockOrderForReplay、resolveAfterConflict）；用户确认。
