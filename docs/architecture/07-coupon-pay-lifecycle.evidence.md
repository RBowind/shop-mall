# 优惠券与订单支付生命周期 Evidence

- GF-7c3fa1d9：下单链路拆为待支付+确认支付+超时取消；券只做满减、自领、一单一张；退款审批退券（过期则置过期）；超时取消不回补购物车；支付时限默认 15 分钟可配置。来源：用户确认。
- GF-e3a07b15：本功能新增表的主键与全部引用列使用 UUID，遵循全局主键口径（见 `08-uuid-primary-keys.md`）。来源：用户确认。
- GF-a48d1e6f：领取接口携带 `Idempotency-Key`（落 `user_coupons.request_id`，`(user_id, request_id)` 部分唯一索引），同键重放不重复发券；生命周期任务扫描周期默认 30 秒。来源：用户确认。
- GF-2e8b40ac：当前 `orders.status` CHECK 为五值（`paid`、`shipped`、`completed`、`refund_requested`、`refunded`）。来源：已核实 `backend/migrations/0001_init.up.sql`（orders 表 `orders_status_valid` 约束）。
- GF-9d14f6b2：当前 `orders.paid_at` 为 `TIMESTAMPTZ NOT NULL DEFAULT now()`，即建单即视为已支付。来源：已核实 `backend/migrations/0001_init.up.sql`。
- GF-5db07e3c：当前库存为 `products.stock` 单列，`CHECK (stock >= 0)`，下单条件更新扣减，无预占列。来源：已核实 `backend/migrations/0001_init.up.sql`（products 表 `products_stock_nonnegative`）与 `backend/internal/product/repository.go`（DecrementStock）。
- GF-b26e19f4：后端进程内现有周期任务仅图片孤儿清理一条，模式为启动 goroutine + `time.Ticker`，随 ctx 退出。来源：已核实 `backend/cmd/server/main.go`（runImageCleanupLoop）。
- GF-4a92c8d1：`request_hash` 现输入为所选购物车项 ID、每项商品与数量、地址 ID 与地址 version，规范化后 SHA-256；本功能将其输入集合扩展 `coupon_id`。来源：已核实 `docs/tech-specs/data-model.md`（orders 表）+ 用户确认的目标设计。
- GF-8f5b3ce2：交易链锁顺序现为 订单行→用户行→地址与购物车行→商品行按 product_id 升序；本功能把券行追加在商品行之后。来源：已核实 `docs/tech-specs/flows.md`（全局事务与并发规则）+ 用户确认的目标设计。
- GF-1c7dd4e8：现有权限码 12 项，超管与运营两内置角色；本功能新增 `coupon:read`、`coupon:write` 并授予两角色。来源：已核实 `docs/tech-specs/data-model.md`（权限域）+ 用户确认的目标设计。
