# 全局主键切换 UUID Evidence

- GF-92f4c07a：全系统所有表的主键 ID 使用 UUID。来源：用户确认。
- GF-0be5d1c8：现有 11 张表主键为 `id BIGSERIAL PRIMARY KEY`（`role_permissions` 为复合主键，无单一 id 列）。来源：已核实 `backend/migrations/0001_init.up.sql`。
- GF-77b2a3e5：部署数据库为 PostgreSQL 16（镜像 `postgres:16-alpine`），本地、联调与演练 compose 同一版本。来源：已核实 `deploy/compose/docker-compose.yml`、`deploy/compose/docker-compose.drill.yml`。
- GF-c19d4f83：多处以主键作为排序次列实现"最新在前"：`idx_products_status_id (status, id DESC)`、`idx_orders_user_status (user_id, status, created_at DESC, id DESC)`、`idx_orders_status_created (status, created_at DESC, id DESC)`、积分流水 `(user_id, id DESC)`。来源：已核实 `backend/migrations/0001_init.up.sql`。
- GF-3e8b62d0：买家列表搜索现为"keyword 数字匹配买家 ID，否则匹配昵称"。来源：已核实 `docs/tech-specs/interfaces.md`（管理员域端点表 `GET /users` 行）。
- GF-6a1f94ce：`INSERT ... ON CONFLICT DO NOTHING RETURNING id`（openid upsert）与按主键 `FOR UPDATE` 行锁为现有写入与并发模式。来源：已核实 `docs/tech-specs/data-model.md`（users 写入语义）、`docs/tech-specs/flows.md`（下单事务）。
- GF-b8e04f27：种子行为 goose SQL 直插（`INSERT ... ON CONFLICT DO NOTHING`，现依赖自增主键默认值，`role_permissions` 按 code/name 反查写入）。来源：已核实 `backend/migrations/0002_seed.up.sql`。
- GF-4d67c9ae：种子行主键在迁移语句内预生成并写死固定 UUID 字面量，一次生成随迁移冻结。来源：用户确认。
- GF-e5910b3d：JWT `sub` 现为 `buyer:<id>` / `admin:<id>` 带前缀格式，前缀是两类令牌互用的解析校验边界；目标口径为前缀保留、ID 段取 UUID 文本。来源：已核实 `backend/internal/middleware/auth.go`（BuyerIDFromSubject / AdminIDFromSubject）+ 用户确认的目标设计。
- GF-f02c8a56：`event_key` 拼接中的 UUID 文本取小写带连字符规范格式，即刻冻结（存量数字键与新键文本空间不重叠）。来源：用户确认。
