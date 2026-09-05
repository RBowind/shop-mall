# 退款

> 买家仅可对未发货订单申请退款；超级管理员审批：通过则退积分、回库存，驳回只回退状态。

## Elaborates

- techspec: `docs/tech-specs/shop-mall-tech-spec.md` §2、§3、§4；`docs/tech-specs/flows.md` 第 3 节；`docs/tech-specs/interfaces.md` 买家域与管理员域端点
- techspec: `docs/architecture/07-coupon-pay-lifecycle.md` §4 主流程 5（退款金额改按实付、新增退券副作用，本节修订的依据）

## MODIFIED Requirements

### Requirement: 申请退款仅限未发货订单

The system SHALL 仅允许买家对本人 paid 订单发起退款申请；已发货（shipped）及之后的状态一律拒绝。

#### Scenario: 申请成功

- **WHEN** 买家对本人 paid 订单请求 `POST /api/v1/orders/{orderId}/refund` 并携带退款原因
- **THEN** 订单状态变 refund_requested，退款原因与申请时间留存且买家可见

#### Scenario: 已发货订单申请

- **WHEN** 买家对 shipped 订单请求退款
- **THEN** 响应 409，状态不变

#### Scenario: 其他状态申请

- **WHEN** 订单处于 `pending_payment`、`cancelled`、completed、refund_requested 或 refunded
- **THEN** 响应 409，状态不变；待支付订单因尚未扣过积分无任何可退，取消订单同理

#### Scenario: 申请他人订单退款

- **WHEN** orderId 不属于当前买家
- **THEN** 响应 404，订单不变

### Requirement: 审批通过退还积分并回补库存

The system SHALL 在审批通过时于同一数据库事务内完成退实付积分、回补可售库存、处置订单占用的券与状态迁移；退款金额由服务端按订单快照计算（实付=原价-券抵扣），审批接口不接收金额。

#### Scenario: 审批通过

- **WHEN** 持 `refund:approve` 的超管对 refund_requested 订单请求 `POST /api/admin/v1/refunds/{orderId}/approve`
- **THEN** 订单状态变 refunded，写入审核人、审核时间与退款完成时间
- **AND** 买家余额增加，金额为该订单的实付积分（`pay_points`，即成交时扣减的积分总额）
- **AND** 写一条关联订单的正值 `order_refund` 流水，留痕审核管理员
- **AND** 订单内每个商品按快照数量回补可售库存
- **AND** 该订单占用的券未过期置回可用、已过期置为过期（券处置口径见 `specs/coupon/spec.md` 状态机）
- **AND** 一条成功审计记录本次审批

#### Scenario: 重复或并发审批

- **WHEN** 订单状态已不是 refund_requested（已被另一请求处理）
- **THEN** 响应 409，不重复退积分、不重复回补库存

#### Scenario: 无审批权限

- **WHEN** 管理员角色不含 `refund:approve`（如运营）
- **THEN** 响应 403，订单不变，且一条失败审计记录该尝试

## ADDED Requirements

### Requirement: 驳回仅回退状态

The system SHALL 驳回时必填原因，订单回退到 paid，不产生任何积分、库存副作用。

#### Scenario: 驳回成功

- **WHEN** 持 `refund:approve` 的超管请求 `POST /api/admin/v1/refunds/{orderId}/reject` 并携带拒绝原因
- **THEN** 订单状态回 paid，写入拒绝原因（买家可见）、审核人与审核时间
- **AND** 买家余额、积分流水、商品库存均不变；订单占用的券保持 `used`，不退回可用

#### Scenario: 缺少拒绝原因

- **WHEN** reject 请求不带拒绝原因
- **THEN** 返回 1xxx 段参数错误，订单保持 refund_requested

#### Scenario: 重复驳回

- **WHEN** 对状态已不是 refund_requested 的订单（如已被驳回回 paid）再次发起 reject
- **THEN** 响应 409，状态、拒绝原因与审核留痕均不变

#### Scenario: 驳回后再次申请

- **WHEN** 订单被驳回到 paid 后，买家再次请求退款
- **THEN** 正常进入 refund_requested

### Requirement: 退款列表查看

The system SHALL 向持 `refund:read` 的管理员提供退款申请中与已退款订单的只读列表。

#### Scenario: 查看列表

- **WHEN** 管理员 `GET /api/admin/v1/refunds`
- **THEN** 返回 refund_requested 与 refunded 状态订单的分页列表，含退款原因

#### Scenario: 无查看权限

- **WHEN** 管理员角色不含 `refund:read`
- **THEN** 响应 403

## Coverage Gaps

- 买家端退款原因在小程序为必选（不想要了/拍错重下/其他+自定义），服务端是否强制非空及长度上限未定义；暂按"服务端接收并留存原因文本"约束。
- "申请时间买家可见"已定为新增 `orders.refund_requested_at` 列，落点见 `contract.md` Schema Changes。
