# 流程：shop-mall

本文件是全系统主流程的详情，入口与鸟瞰图见 [`shop-mall-tech-spec.md`](./shop-mall-tech-spec.md) 第 4 节。流程图与字段名以 [`data-model.md`](./data-model.md) 和 [`../api/openapi.yaml`](../api/openapi.yaml) 为准。

## 全局事务与并发规则

所有主流程共享同一套事务规则（依据 [`../architecture/02-backend.md`](../architecture/02-backend.md)）：

- **单一事务入口**：跨模块副作用由 `internal/application` 用例编排，`db.Transaction` 是唯一提交点；外部网络调用（微信 code2Session）必须在事务外完成，事务提交后才签发令牌或返回成功。
- **交易链锁顺序固定**：订单行（按 `(user_id, client_token)` 定位，下单时即前置重放检查）→ 用户行 → 地址与购物车行 → 商品行按 `product_id` 升序。
- **权限链锁顺序**：角色与管理员账号变更按 管理员行 → 角色行 → 权限映射，业务写与审计同一事务，同样按 `40P01`/`40001` 重试完整事务。
- **条件更新兜底**：库存扣减带 `stock >= qty`、余额扣减带 `points_balance >= amount` 并以 `RETURNING` 取新余额，影响行数为 0 即失败；状态迁移带原状态条件，重复请求不重复写副作用。
- **死锁重试**：仅在完整事务边界重试 PostgreSQL `40P01`（死锁）与 `40001`（序列化失败），重试是重新执行整个用例，不重试单条语句。
- **异步任务**：账务与订单链路无异步补偿任务；唯一的周期任务是未引用图片延迟清理（上传失败或替换图片留下的孤儿文件，按 `IMAGE_CLEANUP_INTERVAL` 周期执行）。历史数据处理仅发生在新增非空字段时，按 expand/backfill/contract 迁移。

## 1. 微信登录与首次赠分

触发：小程序调用 `wx.login()` 取得一次性 code（code 是一次性登录凭证，微信侧只能消费一次），请求 `POST /api/v1/auth/wx-login`。

```mermaid
sequenceDiagram
    participant M as 小程序
    participant B as Go 后端
    participant W as 微信服务器
    participant DB as PostgreSQL

    M->>B: POST /api/v1/auth/wx-login（携带 code）
    B->>W: code2Session（事务外）
    alt code2Session 失败
        W-->>B: 错误
        B-->>M: 2xxx 认证错误，不建用户
        M->>M: 重新 wx.login 获取新 code
    else 成功
        W-->>B: openid + session_key
        Note over B: session_key 不落库、不下发、不写日志
        B->>DB: 开启事务
        B->>DB: INSERT users ON CONFLICT DO NOTHING RETURNING id
        alt 真正插入（新用户）
            B->>DB: 赠 SIGNUP_BONUS_POINTS（>0 才更新余额）
            B->>DB: 写 signup_bonus 流水（event_key = signup:{user_id}）
        else 已存在（老用户）
            B->>DB: 不赠分
        end
        B->>DB: 提交事务
        alt 提交失败
            B-->>M: 不签发令牌，重新登录
        else 提交成功
            B-->>M: 买家 JWT
        end
    end
```

- 赠分的新用户判定与"每用户至多一次"，由"只有真正插入用户的请求执行赠分" + `signup_bonus` 部分唯一索引共同保证。
- `SIGNUP_BONUS_POINTS=0` 时不更新余额、不写流水。
- code 在微信侧只能消费一次；网络超时不得盲目重放同一个 code，客户端重新 `wx.login()`。

## 2. 下单（幂等）

触发：买家在确认页提交，请求 `POST /api/v1/orders`，Header 携带客户端生成的 UUID 幂等键 `Idempotency-Key`（映射到 `orders.client_token`）。

```mermaid
sequenceDiagram
    participant M as 小程序
    participant U as 下单用例
    participant DB as PostgreSQL

    M->>U: POST /api/v1/orders + Idempotency-Key
    U->>U: 校验 token 格式与请求参数，user_id 只取 JWT
    U->>DB: 开启事务，SELECT FOR UPDATE 查询并锁定 (user_id, client_token) 已有订单
    alt 已有订单
        U->>U: 比较 request_hash
        alt hash 相同
            U-->>M: 返回原订单，HTTP 200，无副作用
        else hash 不同
            U-->>M: HTTP 409 幂等冲突
        end
    else 无已有订单（新请求）
        U->>DB: 锁用户行
        U->>DB: 校验并锁定地址与所选购物车项（越权整体回滚）
        U->>DB: 按 product_id 升序锁商品，读最新价格与库存
        U->>DB: 普通 INSERT 订单（含 client_token 与 request_hash）
        U->>DB: 条件扣库存（stock >= qty）
        U->>DB: 条件扣积分（points_balance >= total），RETURNING 新余额
        U->>DB: 写订单明细与 order_pay 流水（event_key = order_pay:{order_id}）
        U->>DB: 删除所选购物车项（与订单同事务）
        alt 任一步失败
            U->>DB: 整体回滚，token 不固化，可重试
            U-->>M: 库存不足 / 积分不足 / 业务错误
        else 全部成功
            U->>DB: 提交事务
            U-->>M: 订单号，状态 paid
        end
    end
```

- `request_hash` 输入为所选购物车项 ID、每项商品与数量、地址 ID 与地址 `version`（规范化后 SHA-256）；价格与库存不进 hash，由事务内实时读取。
- 同 token 两个首次请求并发时都能通过前置检查，后到者的 INSERT 撞 `(user_id, client_token)` 唯一约束、整体回滚，随后在事务外重读获胜订单，按 `request_hash` 答复 200 重放或 409 冲突。`order_no` 冲突单独处理，不得误判为幂等重放，其他唯一约束冲突也不得解释成重放。
- 客户端纪律：用户修改商品、数量或地址后必须换新 token；网络重试复用原 token。

## 3. 退款申请与审批

触发：买家对 paid 订单申请退款（`POST /api/v1/orders/{orderId}/refund`）；管理员审批（`POST /api/admin/v1/refunds/{orderId}/approve` 或 `/reject`，权限 `refund:approve`）。

```mermaid
sequenceDiagram
    participant M as 小程序
    participant U as 退款用例
    participant A as 管理后台
    participant DB as PostgreSQL

    M->>U: POST /api/v1/orders/{orderId}/refund
    U->>DB: UPDATE orders SET status='refund_requested', refund_reason=... WHERE id=? AND user_id=? AND status='paid'
    alt 影响行数 0
        U-->>M: 404（不存在或非本人）或 409（状态不符）
    else 迁移成功
        U-->>M: 申请已提交
    end

    A->>U: POST /api/admin/v1/refunds/{orderId}/approve
    U->>DB: 开启事务
    U->>DB: 条件抢占 refund_requested -> refunded，写 refund_reviewed_by/at 与 refunded_at，RETURNING id, user_id
    alt 抢占成功
        U->>DB: 条件回补积分，写 order_refund 流水（event_key = order_refund:{order_id}）
        U->>DB: 按 product_id 升序回补库存
        U->>DB: 提交事务
        U-->>A: 审批完成
    else 未抢到（并发审批 / 重复审批）
        U->>DB: 回滚，不执行任何副作用
        U-->>A: 409 当前状态
    end

    A->>U: POST /api/admin/v1/refunds/{orderId}/reject
    U->>DB: 条件迁移 refund_requested -> paid，写 refund_reject_reason 与审核人、时间
    U-->>A: 驳回完成（不动余额、不写流水、不回补库存）
```

审批通过的积分退回、流水写入、库存回补与状态抢占在同一事务；只有成功抢占状态的请求可执行副作用。退款金额由后端按订单快照计算，审批接口不接收金额。

## 4. 发货与确认收货

触发：管理员发货（`POST /api/admin/v1/orders/{orderId}/ship`，权限 `order:ship`）；买家确认收货（`POST /api/v1/orders/{orderId}/confirm`）。

```mermaid
sequenceDiagram
    participant A as 管理后台
    participant U as 订单用例
    participant M as 小程序
    participant DB as PostgreSQL

    A->>U: POST /api/admin/v1/orders/{orderId}/ship
    U->>DB: 按 id 读订单行
    alt 订单不存在
        U-->>A: 404
    else 存在
        U->>DB: UPDATE orders SET status='shipped', shipped_by=JWT 中管理员 ID, shipped_at=now WHERE id=? AND status='paid'
        alt 影响行数 0
            U-->>A: 409（非 paid 或已发过），不重复写操作人与时间
        else 成功
            U-->>A: 发货成功
        end
    end

    M->>U: POST /api/v1/orders/{orderId}/confirm
    U->>DB: UPDATE orders SET status='completed', completed_at=now WHERE id=? AND user_id=? AND status='shipped'
    alt 影响行数 0
        U-->>M: 404（非本人订单）或 409（状态不符）
    else 成功
        U-->>M: 确认收货完成
    end
```

`shipped_by` 只取管理员令牌，不信任请求体；本期不采集物流单号。

## 5. 管理员登录与鉴权

触发：后台提交用户名密码（`POST /api/admin/v1/auth/login`）。

```mermaid
sequenceDiagram
    participant A as 管理后台
    participant B as Go 后端
    participant DB as PostgreSQL

    A->>B: POST /api/admin/v1/auth/login
    B->>B: 按账号与 IP 双维度限速
    B->>DB: 读 admin_users，Argon2id 校验密码
    alt 失败
        B-->>A: 统一错误（不区分账号不存在与密码错误），写审计
    else 成功
        B->>DB: 更新 last_login_at，写审计
        B-->>A: Set-Cookie admin_access_token（HttpOnly · Secure · SameSite，30 分钟）+ CSRF token
    end

    A->>B: 写请求带 X-CSRF-Token
    B->>B: 校验签名算法 / iss / aud / exp / kid / enabled / token_version，实时读权限码
    alt 校验不过或权限不足
        B-->>A: 401 或 403
    else 通过
        B-->>A: 执行业务
    end
```

令牌载荷含 `sub`、`iss`、`aud`、`iat`、`exp`、`jti`、`kid`、`token_version`，不含权限列表。改密或禁用递增 `token_version`，旧令牌下一次请求即失效。登录是 CSRF 例外（此时无 Cookie），靠 HTTPS、SameSite、Origin 校验与限流保护。

## 6. 管理员积分调整

触发：超管提交调整（`POST /api/admin/v1/points/adjust`，权限 `points:adjust`，携带 `Idempotency-Key`）。

```mermaid
sequenceDiagram
    participant A as 管理后台
    participant U as 积分用例
    participant DB as PostgreSQL

    A->>U: POST /api/admin/v1/points/adjust + Idempotency-Key
    U->>DB: 开启事务，锁用户行
    U->>U: 派生 event_key = admin_adjust:{admin_id}:{idempotency_key}（幂等域为操作管理员），计算 request_hash
    U->>DB: 按 event_key 查已有流水并比较 request_hash
    alt 已存在且 hash 相同
        U-->>A: 重放原流水结果
    else 已存在且 hash 不同
        U-->>A: 409
    else 不存在
        U->>DB: 条件更新余额（结果 >= 0），RETURNING 新余额
        U->>DB: 写 admin_adjust 流水（remark 非空、留痕操作管理员）
        U->>DB: 同一事务写成功审计
        U->>DB: 提交事务
        U-->>A: 调整完成
    end
```

- 流水、余额更新与成功审计在同一事务提交；被拒绝的请求（409、余额方向非法）在回滚后尽力补记失败审计，补记本身失败不改变对调用方的答复。
- 管理员不能直接修改余额字段；delta 可正可负，方向由余额条件更新兜底为非负。

