# 数据库设计（PostgreSQL）

总览级 ER 和关键时序见 `00-overview.md`，接口字段见 `../api/openapi.yaml`。本文是建表级设计和迁移评审依据。落地形式为 goose migration；已执行的 migration 文件禁止修改。

## 1. 设计原则

- 积分、价格和库存一律使用整数，积分和余额使用 BIGINT。
- 订单保存商品和地址快照，历史订单不依赖可变商品和地址。
- 状态使用 VARCHAR + CHECK；合法迁移在 service 条件更新中保证。
- 跨表业务一致性由 application usecase 事务保证，数据库约束负责最后一道防线。
- 账务流水只追加、不修改、不删除；修正通过反向或调整流水完成。
- 生产回滚只回滚应用，不使用 `goose down` 删除生产数据。

## 2. 表结构

管理员相关表先创建，供订单和流水表建立外键。

### 2.1 roles / permissions / admin_users — 后台账号与权限

```sql
CREATE TABLE roles (
    id     BIGSERIAL PRIMARY KEY,
    name   VARCHAR(32) NOT NULL UNIQUE,
    remark VARCHAR(128) NOT NULL DEFAULT ''
);

CREATE TABLE permissions (
    id   BIGSERIAL PRIMARY KEY,
    code VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(64) NOT NULL
);

CREATE TABLE admin_users (
    id             BIGSERIAL PRIMARY KEY,
    username       VARCHAR(32)  NOT NULL UNIQUE,
    password_hash  VARCHAR(255) NOT NULL,
    token_version  BIGINT       NOT NULL DEFAULT 1 CHECK (token_version > 0),
    role_id        BIGINT       NOT NULL REFERENCES roles(id),
    enabled        BOOLEAN      NOT NULL DEFAULT true,
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
    role_id       BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);
```

权限种子为：`product:read`、`product:write`、`image:write`、`order:read`、`order:ship`、`refund:read`、`refund:approve`、`points:adjust`、`role:manage`、`admin:self`。

`0002_seed.up.sql` 只写角色、权限和角色映射，不读取环境变量、不生成密码哈希、不覆盖管理员密码。管理员由一次性 bootstrap 命令创建。

### 2.2 users — 买家

```sql
CREATE TABLE users (
    id             BIGSERIAL PRIMARY KEY,
    openid         VARCHAR(64) NOT NULL UNIQUE,
    nickname       VARCHAR(64) NOT NULL DEFAULT '',
    avatar_url     TEXT        NOT NULL DEFAULT '',
    points_balance BIGINT      NOT NULL DEFAULT 0 CHECK (points_balance >= 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`users.id` 是内部主键，`openid` 是当前小程序内的微信登录唯一键。业务表只通过 `user_id` 关联。

### 2.3 products — 商品

```sql
CREATE TABLE products (
    id           BIGSERIAL PRIMARY KEY,
    name         VARCHAR(128) NOT NULL,
    description  TEXT         NOT NULL DEFAULT '',
    main_image   TEXT         NOT NULL DEFAULT '',
    price_points BIGINT       NOT NULL CHECK (price_points > 0),
    stock        INT          NOT NULL DEFAULT 0 CHECK (stock >= 0),
    status       VARCHAR(16)  NOT NULL DEFAULT 'on_sale'
                 CHECK (status IN ('on_sale', 'off_sale')),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_status_id
    ON products (status, id DESC);
```

`main_image` 保存图片 object key，不保存完整 URL。商品下架代替物理删除，避免订单明细失去商品引用。

### 2.4 user_addresses — 收货地址

```sql
CREATE TABLE user_addresses (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receiver   VARCHAR(32) NOT NULL,
    phone      VARCHAR(20) NOT NULL,
    region     VARCHAR(64) NOT NULL,
    detail     VARCHAR(255) NOT NULL,
    is_default BOOLEAN     NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_addresses_user ON user_addresses (user_id, id);

CREATE UNIQUE INDEX uq_default_address
    ON user_addresses (user_id)
    WHERE is_default = true;
```

设置默认地址时在同一事务内取消旧默认地址，再设置新地址；部分唯一索引是并发兜底。

### 2.5 cart_items — 购物车

```sql
CREATE TABLE cart_items (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_id BIGINT NOT NULL REFERENCES products(id),
    quantity   INT    NOT NULL CHECK (quantity > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, product_id)
);
```

重复加购由 service 累加数量；商品下架不删除购物车记录，读取时提示商品不可购买。

### 2.6 orders — 订单

```sql
CREATE TABLE orders (
    id            BIGSERIAL PRIMARY KEY,
    order_no      VARCHAR(32) NOT NULL UNIQUE,
    user_id       BIGINT      NOT NULL REFERENCES users(id),
    client_token  VARCHAR(64) NOT NULL,
    request_hash  CHAR(64)    NOT NULL,
    status        VARCHAR(20) NOT NULL
                  CHECK (status IN ('paid', 'shipped', 'completed',
                                    'refund_requested', 'refunded')),
    total_points  BIGINT      NOT NULL CHECK (total_points > 0),
    receiver      VARCHAR(32) NOT NULL,
    phone         VARCHAR(20) NOT NULL,
    address       VARCHAR(512) NOT NULL,
    refund_reason VARCHAR(255) NOT NULL DEFAULT '',
    refund_reject_reason VARCHAR(255) NOT NULL DEFAULT '',
    refund_reviewed_by BIGINT REFERENCES admin_users(id) ON DELETE RESTRICT,
    refund_reviewed_at TIMESTAMPTZ,
    paid_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    shipped_by    BIGINT REFERENCES admin_users(id) ON DELETE RESTRICT,
    shipped_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    refunded_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, client_token),
    CHECK (btrim(client_token) <> ''),
    CHECK (
        (status IN ('shipped', 'completed')
            AND shipped_by IS NOT NULL
            AND shipped_at IS NOT NULL)
        OR
        (status NOT IN ('shipped', 'completed')
            AND shipped_by IS NULL
            AND shipped_at IS NULL)
    ),
    CHECK ((status = 'completed') = (completed_at IS NOT NULL)),
    CHECK ((status = 'refunded') = (refunded_at IS NOT NULL))
);

CREATE INDEX idx_orders_user_status
    ON orders (user_id, status, created_at DESC, id DESC);

CREATE INDEX idx_orders_status_created
    ON orders (status, created_at DESC, id DESC);
```

`request_hash` 由服务端对地址版本/快照、商品 ID 和数量规范化后计算 SHA-256。价格和库存不进入 hash，由服务端事务内读取。

### 2.7 order_items — 订单明细

```sql
CREATE TABLE order_items (
    id             BIGSERIAL PRIMARY KEY,
    order_id       BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id     BIGINT NOT NULL REFERENCES products(id),
    product_name   VARCHAR(128) NOT NULL,
    product_image  TEXT NOT NULL DEFAULT '',
    price_snapshot BIGINT NOT NULL CHECK (price_snapshot > 0),
    quantity       INT NOT NULL CHECK (quantity > 0),
    UNIQUE (order_id, product_id)
);

CREATE INDEX idx_order_items_order ON order_items (order_id);
```

商品删除默认被外键阻止；商品使用下架代替删除。订单明细保存商品名称、图片、价格和数量快照。

### 2.8 points_ledger — 积分流水

```sql
CREATE TABLE points_ledger (
    id                 BIGSERIAL PRIMARY KEY,
    user_id            BIGINT NOT NULL REFERENCES users(id),
    order_id           BIGINT REFERENCES orders(id),
    event_key          VARCHAR(96) NOT NULL UNIQUE,
    request_hash       CHAR(64),
    type               VARCHAR(20) NOT NULL
                       CHECK (type IN ('order_pay', 'order_refund',
                                       'signup_bonus', 'admin_adjust')),
    delta              BIGINT NOT NULL CHECK (delta <> 0),
    balance_after      BIGINT NOT NULL CHECK (balance_after >= 0),
    created_by_admin_id BIGINT REFERENCES admin_users(id) ON DELETE RESTRICT,
    remark             VARCHAR(255) NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (type = 'order_pay'
            AND order_id IS NOT NULL
            AND delta < 0
            AND created_by_admin_id IS NULL)
        OR
        (type = 'order_refund'
            AND order_id IS NOT NULL
            AND delta > 0
            AND created_by_admin_id IS NOT NULL)
        OR
        (type = 'signup_bonus'
            AND order_id IS NULL
            AND delta > 0
            AND created_by_admin_id IS NULL)
        OR
        (type = 'admin_adjust'
            AND order_id IS NULL
            AND created_by_admin_id IS NOT NULL)
    ),
    CHECK (type <> 'admin_adjust' OR btrim(remark) <> ''),
    CHECK (
        (type = 'admin_adjust' AND request_hash IS NOT NULL)
        OR (type <> 'admin_adjust' AND request_hash IS NULL)
    )
);

CREATE INDEX idx_ledger_user_id ON points_ledger (user_id, id DESC);
CREATE INDEX idx_ledger_order ON points_ledger (order_id);

CREATE UNIQUE INDEX uq_signup_bonus_user
    ON points_ledger (user_id)
    WHERE type = 'signup_bonus';

CREATE UNIQUE INDEX uq_order_pay
    ON points_ledger (order_id)
    WHERE type = 'order_pay';

CREATE UNIQUE INDEX uq_order_refund
    ON points_ledger (order_id)
    WHERE type = 'order_refund';

CREATE TABLE audit_logs (
    id             BIGSERIAL PRIMARY KEY,
    actor_admin_id BIGINT REFERENCES admin_users(id) ON DELETE RESTRICT,
    actor_role     VARCHAR(32) NOT NULL,
    action         VARCHAR(64) NOT NULL,
    target_type    VARCHAR(64) NOT NULL,
    target_id      BIGINT,
    result         VARCHAR(16) NOT NULL CHECK (result IN ('success', 'failure')),
    before_data    JSONB,
    after_data     JSONB,
    trace_id       VARCHAR(128) NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_actor_time ON audit_logs (actor_admin_id, created_at DESC);
CREATE INDEX idx_audit_target_time ON audit_logs (target_type, target_id, created_at DESC);
```

业务事件键由服务端生成：`signup:{user_id}`、`order_pay:{order_id}`、`order_refund:{order_id}`、`admin_adjust:{admin_id}:{idempotency_key}`（管理员调整的幂等域为操作管理员）。管理员积分调整的 `request_hash` 对规范化的买家、delta 和备注计算 SHA-256；同 key 不同 hash 返回 409。部分唯一索引防止 event_key 生成错误时重复记账。

`audit_logs` 只允许追加，`before_data` 和 `after_data` 只保存脱敏后的变更摘要，不保存密码、JWT、session_key、完整手机号和地址。

## 3. 并发与一致性

### 3.1 下单扣库存和积分

```sql
UPDATE products
SET stock = stock - $qty, updated_at = now()
WHERE id = $pid AND stock >= $qty;

UPDATE users
SET points_balance = points_balance - $amount, updated_at = now()
WHERE id = $uid AND points_balance >= $amount
RETURNING points_balance;
```

影响行数为 0 时分别返回库存不足或积分不足。两条更新、订单、明细和流水必须处于同一个事务。

### 3.2 幂等订单

重放检查前置：事务内 `SELECT ... FOR UPDATE` 查询 `(user_id, client_token)` 已有订单——存在且 `request_hash` 相同返回原订单，存在且不同返回幂等冲突；不存在则普通 INSERT 创建订单并继续副作用。

同 token 的两个首次并发请求都可通过前置检查，后到者的 INSERT 撞 `(user_id, client_token)` 唯一约束、整体回滚，再在事务外重读获胜订单按 `request_hash` 答复重放或冲突。

- 业务失败整体回滚，订单和 token 不保留。
- `order_no` 冲突必须单独处理，不能被解释成 client_token 冲突。

### 3.3 退款审批

```sql
UPDATE orders
SET status = 'refunded',
    refund_reviewed_by = $admin_id,
    refund_reviewed_at = now(),
    refunded_at = now(),
    updated_at = now()
WHERE id = $order_id
  AND status = 'refund_requested'
RETURNING id, user_id;
```

影响行数为 0 时不得执行积分退回、流水写入或库存回补。

### 3.4 对账

```sql
SELECT u.id, u.points_balance,
       COALESCE(SUM(l.delta), 0) AS ledger_balance
FROM users u
LEFT JOIN points_ledger l ON l.user_id = u.id
GROUP BY u.id, u.points_balance
HAVING u.points_balance <> COALESCE(SUM(l.delta), 0);
```

对账任务发现差异时只告警并冻结相关积分调整，不自动修改余额或删除流水。

## 4. 迁移管理

- `0001_init.up.sql` 按管理员表、用户/商品表、订单表、流水表顺序创建外键完整的初始结构。
- `0002_seed.up.sql` 只种子角色、权限和权限映射；管理员密码由 bootstrap 命令处理。
- 如果 `0001` 已在任一环境执行，禁止修改原文件，新增 migration 分阶段增加字段、回填和约束。
- 新增 `NOT NULL` 字段按 expand/backfill/contract 执行，避免旧版本应用无法启动。
- 迁移前做数据库和图片卷备份；生产失败不执行自动 down migration。
- migration 版本、应用版本和回滚结果写入发布记录。

## 5. 评审清单

1. 索引：公开商品、用户订单、后台订单、流水列表和唯一事件是否覆盖查询。
2. 类型：积分、价格、库存、数量是否为整数，CHECK 是否挡住非法方向和负数。
3. 状态：状态 CHECK、service 白名单和条件 UPDATE 是否一致。
4. 幂等：订单和每类积分事件是否有业务唯一键，冲突是否不会重复执行副作用。
5. 外键：操作人、用户、订单、商品的引用和删除策略是否明确。
6. 并发：用户行、订单行和商品行的锁顺序是否固定，40P01/40001 是否重试完整事务。
7. 审计：余额、流水、操作人、备注和状态时间是否可追溯。
8. 恢复：数据库、图片卷、配置和 Secret 是否能按 Runbook 恢复。
