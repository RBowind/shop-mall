# Tech Spec：全局主键切换 UUID

| 字段 | 值 |
|---|---|
| PRD 来源 | 无（用户确认的全局数据模型决策） |
| 关联 ticket | 无 |
| 负责服务 | backend / miniapp / admin |
| 状态 | 草稿 |
| 更新日期 | 2026-09-03 |

## 1. 背景与目标

全部业务表的主键是数据库自增 `BIGSERIAL`，资源标识从 1 递增、可被外部枚举预测，并以十进制字符串的形式穿过接口、两端代码和测试。本功能把全系统主键与引用列统一为 UUID，主键值改由服务层生成（UUIDv7，时间有序版本），列上不再保留自增序列。

用户结果：所有资源 ID 变为 UUID 字符串；除 ID 的形态与校验外，全部业务行为、页面流程与状态语义不变。

成功标准：迁移后后端全量测试（单元/集成/e2e/安全）与两端测试全绿；列表"最新在前"的排序结果与迁移前的自增语义一致；接口只接受 UUID 形式的资源 ID 输入。

范围限制：`order_no`、`event_key`、`client_token`、`request_hash` 保持字符串列，生成规则不变（其中拼接或规范化输入的资源 ID 按本规格 §3 口径变化）；不做短码/hashid 转换层；数据库维持 PostgreSQL 16，UUIDv7 由 Go 服务层生成，不依赖数据库函数。

## 2. 功能需求

- 后端服务在插入任何记录前自行生成 UUIDv7 作为主键；写入路径不再使用自增序列。
- 端点的路径参数、查询参数与请求体中的资源 ID 一律按 UUID 字符串解析与校验；非 UUID 格式按参数错误拒绝。
- 商品公开列表、买家与管理员订单列表、积分流水、我的券（`07` 口径）的"最新在前"排序行为与迁移前一致；实现口径见 §3。
- 迁移发布后两端既有登录态立即失效，客户端重新登录恢复（口径见 §3）。
- 两端由 openapi 重新生成类型并消费，接口 ID 字段全部输出 UUID 字符串（见 §5）。
- 存量数据需要迁移为主键 UUID 形态后才能启用新代码（含全部外键引用与关联字段）；执行方式与回填策略不在本规格范围。

## 3. 数据模型

统一规则：11 张带自增主键的表（`roles`、`permissions`、`admin_users`、`users`、`products`、`user_addresses`、`cart_items`、`orders`、`order_items`、`points_ledger`、`audit_logs`）主键列 `id` 改为 `UUID NOT NULL`，无默认值，主键值来源=服务生成（UUIDv7）；所有引用这些主键的外键列与 ID 语义列同步改 `UUID`。`role_permissions` 为复合主键（`role_id`、`permission_id` 两列改 UUID），无单一 `id`。实体、关系、写入语义（insert / upsert / append / 条件更新）均不变，仅列类型与取值来源变化。

主键必须取时间有序的 UUIDv7，不得用随机 v4："最新在前"的列表语义依赖主键值随时间递增，现有 `(status, id DESC)`、`(user_id, status, created_at DESC, id DESC)`、`(user_id, id DESC)` 等以主键为次列的索引排序行为由此保留。

种子行（`roles`、`permissions` 及授予关系）由 goose SQL 直插，不经过服务层生成器：迁移语句内预生成并写死固定 UUID 字面量作为这些行的主键，一次生成、随迁移冻结；`role_permissions` 继续按 `code` / `name` 反查两端 ID 写入。`07` 新增两个权限码的种子同此口径。

受影响的 ID 语义列（决策性列举）：

| 列 | 现类型 | 目标口径 |
|---|---|---|
| `audit_logs.target_id` | BIGINT | UUID；泛目标引用与主键同型 |
| `points_ledger.order_id`、`created_by_admin_id` | BIGINT | UUID |
| `orders.refund_reviewed_by`、`shipped_by` | BIGINT | UUID |
| 指向上述主键的其余全部外键列（`user_id`、`role_id`、`product_id` 等） | BIGINT | UUID |

其余联动口径：

- `points_ledger.event_key`：拼接模板不变（`signup:{user_id}`、`order_pay:{order_id}`、`order_refund:{order_id}`、`admin_adjust:{admin_id}:{idempotency_key}`），其中 ID 取 UUID 文本（小写、带连字符的规范格式）；存量流水的旧数字键不回写，新旧键文本空间不重叠，部分唯一索引继续生效。
- `orders.request_hash`：规范化输入中的购物车项 ID、商品 ID、地址 ID 改为 UUID 文本参与编码（`coupon_id` 若存在，按 `07` 口径一并加入）；编码在发布前按主档冻结条款冻结。
- JWT：`sub` 保留现有 `buyer:` / `admin:` 前缀（前缀是两类令牌互用的解析校验边界），前缀后的 ID 段取 UUID 文本；`token_version`、`kid`、双密钥环隔离等机制不变。迁移发布后存量未过期令牌因 `sub` 解析失败一律拒绝：买家端重新静默登录恢复，管理端重新账号登录恢复。
- 索引：现有 `(status, id DESC)`、`(user_id, status, created_at DESC, id DESC)`、`(user_id, id DESC)` 等以主键为次列的索引保留定义，列类型随主键变更；UUIDv7 的时间有序性是"最新在前"语义成立的依据。
- 外键 ON DELETE 行为（CASCADE/RESTRICT）与 CHECK 约束全部保留。

迁移要求：每张存量表的主键与全部引用列需要换算为一致的 UUID 值（同一行的主键新值被所有指向它的行引用；`users`、`products`、`orders`、`admin_users` 是换算图的根）。快照类文本列（`product_name`、地址快照等）不受影响。

## 4. 流程

本功能不新增业务流程、状态机或异步任务：`../tech-specs/flows.md` 各主流程的时序、事务边界、锁顺序、条件更新与幂等规则全部保持不变，变化的只是穿过流程的 ID 取值与类型。

统一的新记录创建语义（对全部 insert 路径）：服务层在事务内执行 INSERT 前生成 UUIDv7，将主键与外键值随业务字段一次写入；`INSERT ... ON CONFLICT DO NOTHING RETURNING id`（openid upsert）等现有语句形态保留，`RETURNING id` 返回的是服务预生成的值。`FOR UPDATE` 行锁按主键定位，UUID 的字节序比较是确定性的，商品行按 `product_id` 升序加锁的防死锁规则照常成立。

无异步补偿任务；历史数据换算与回填的执行方式不在本规格范围。

## 5. 接口契约

不新增端点；对现有端点做全量 ID 口径变更，字段级定义更新 `../api/openapi.yaml` 后重新生成两端类型。

契约变更口径（适用于所有含 ID 的端点，买家域与管理域同）：

- 路径参数与查询参数：`{productId}`、`{orderId}`、`{itemId}`、`{addressId}`、`{templateId}`（`07` 新增）、`{roleId}`、`{adminUserId}` 等一律为 UUID 字符串；非 UUID 格式返回参数错误（1000-1999 段）。
- 请求体：资源 ID 字段（`cart_item_ids`、`address_id`、下单 `coupon_id`、管理员改角色的 `role_id`）取 UUID 文本，替换现十进制字符串口径；角色权限分配按权限码字符串数组提交，不经此迁移。
- 响应体：所有 `id`、`*_id` 引用字段输出 UUID 字符串。
- 买家列表搜索（`GET /api/admin/v1/users?keyword=`）：现行为"数字匹配买家 ID"改为"keyword 为合法 UUID 文本时按买家 ID 精确匹配，否则匹配昵称"。
- 错误响应不回显内部 SQL 值；UUID 解析失败不区分"不存在"与"格式错误"以外的额外语义（格式错误归参数错误）。

统一响应包裹、分页参数、鉴权与错误码分段不变。
