# 发货与收货

> 管理员对已支付订单发货，买家对已发货订单确认收货。发货记录操作人与时间，确认收货记录完成时间，非法状态被拒。

## Elaborates

- techspec: `docs/tech-specs/shop-mall-tech-spec.md` §2、§4；`docs/tech-specs/flows.md` 第 4 节；`docs/tech-specs/interfaces.md` 管理员域端点
- techspec: `docs/architecture/07-coupon-pay-lifecycle.md` §4（订单状态机新增 `pending_payment`、`cancelled` 两态，本节枚举补全的依据）

## ADDED Requirements

### Requirement: 管理员发货

The system SHALL 仅允许持有 `order:ship` 权限的管理员把 paid 订单迁移为 shipped，发货人取自登录令牌。

#### Scenario: 发货成功

- **WHEN** 持 `order:ship` 的管理员请求 `POST /api/admin/v1/orders/{orderId}/ship`，订单状态为 paid
- **THEN** 订单状态变 shipped，写入发货人（取当前认证管理员的身份，请求体中的发货人字段一概不采信）与发货时间
- **AND** 一条成功审计记录本次操作
- **AND** 本期无物流单号：接口与展示都不采集

#### Scenario: 状态不符

- **WHEN** 订单状态不是 paid（shipped、completed、refund_requested、refunded）
- **THEN** 响应 409，发货人与发货时间不被改写

#### Scenario: 订单不存在

- **WHEN** orderId 在系统中不存在
- **THEN** 响应 404

#### Scenario: 无权限管理员

- **WHEN** 管理员角色不含 `order:ship`
- **THEN** 响应 403，且一条失败审计记录该尝试

### Requirement: 买家确认收货

The system SHALL 仅允许买家本人对 shipped 订单确认收货，确认后订单进入终态 completed。

#### Scenario: 确认成功

- **WHEN** 买家对本人 shipped 订单请求 `POST /api/v1/orders/{orderId}/confirm`
- **THEN** 订单状态变 completed，写入完成时间

#### Scenario: 其他状态确认

- **WHEN** 订单处于 paid、completed、refund_requested 或 refunded
- **THEN** 响应 409，状态与时间不变

#### Scenario: 重复确认

- **WHEN** 买家对已 completed 的订单再次确认
- **THEN** 响应 409，不重复写完成时间

#### Scenario: 确认他人订单

- **WHEN** orderId 不属于当前买家
- **THEN** 响应 404

### Requirement: 管理端订单查询

The system SHALL 向持 `order:read` 的管理员提供全量订单列表与详情，收货信息按脱敏规则展示。

#### Scenario: 列表查询

- **WHEN** 管理员 `GET /api/admin/v1/orders` 带状态筛选
- **THEN** 返回全量匹配订单的分页列表，含脱敏后的收货信息

#### Scenario: 详情查询

- **WHEN** 管理员 `GET /api/admin/v1/orders/{orderId}`
- **THEN** 返回该订单详情，含脱敏后的收货信息与各节点时间

#### Scenario: 无权限查询

- **WHEN** 管理员角色不含 `order:read`
- **THEN** 响应 403

#### Scenario: 查询不存在的订单

- **WHEN** orderId 不存在
- **THEN** 响应 404

## MODIFIED Requirements

### Requirement: 管理员发货

The system SHALL 仅允许持有 `order:ship` 权限的管理员把 paid 订单迁移为 shipped，发货人取自登录令牌；支付完成前的订单（`pending_payment`）与已取消订单（`cancelled`）一律不可发货。

#### Scenario: 发货成功

- **WHEN** 持 `order:ship` 的管理员请求 `POST /api/admin/v1/orders/{orderId}/ship`，订单状态为 paid
- **THEN** 订单状态变 shipped，写入发货人（取当前认证管理员的身份，请求体中的发货人字段一概不采信）与发货时间
- **AND** 一条成功审计记录本次操作
- **AND** 本期无物流单号：接口与展示都不采集

#### Scenario: 状态不符

- **WHEN** 订单状态不是 paid（`pending_payment`、`cancelled`、shipped、completed、refund_requested、refunded）
- **THEN** 响应 409，发货人与发货时间不被改写

#### Scenario: 订单不存在

- **WHEN** orderId 在系统中不存在
- **THEN** 响应 404

#### Scenario: 无权限管理员

- **WHEN** 管理员角色不含 `order:ship`
- **THEN** 响应 403，且一条失败审计记录该尝试

### Requirement: 买家确认收货

The system SHALL 仅允许买家本人对 shipped 订单确认收货，确认后订单进入终态 completed。

#### Scenario: 确认成功

- **WHEN** 买家对本人 shipped 订单请求 `POST /api/v1/orders/{orderId}/confirm`
- **THEN** 订单状态变 completed，写入完成时间

#### Scenario: 其他状态确认

- **WHEN** 订单处于 `pending_payment`、`cancelled`、paid、completed、refund_requested 或 refunded
- **THEN** 响应 409，状态与时间不变

#### Scenario: 重复确认

- **WHEN** 买家对已 completed 的订单再次确认
- **THEN** 响应 409，不重复写完成时间

#### Scenario: 确认他人订单

- **WHEN** orderId 不属于当前买家
- **THEN** 响应 404

### Requirement: 管理端订单查询

The system SHALL 向持 `order:read` 的管理员提供全量订单列表与详情，状态筛选覆盖七值，收货信息按脱敏规则展示。

#### Scenario: 列表查询

- **WHEN** 管理员 `GET /api/admin/v1/orders` 带状态筛选
- **THEN** 返回全量匹配订单的分页列表（筛选值含 `pending_payment` 与 `cancelled`），含脱敏后的收货信息

#### Scenario: 详情查询

- **WHEN** 管理员 `GET /api/admin/v1/orders/{orderId}`
- **THEN** 返回该订单详情，含脱敏后的收货信息与各节点时间；待支付订单含支付截止时间，已取消订单含取消时间

#### Scenario: 无权限查询

- **WHEN** 管理员角色不含 `order:read`
- **THEN** 响应 403

#### Scenario: 查询不存在的订单

- **WHEN** orderId 不存在
- **THEN** 响应 404

## Coverage Gaps

- 收货信息脱敏的具体格式（如手机号保留哪些位）未定义，沿用 techspec follow-up FU-1c6f9b3e，待产品拍板。
- 无自动确认收货：订单停在 shipped 多久都不自动终态（PRD 明确不做项）。
