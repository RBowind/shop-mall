# 积分账务

> 余额与流水在同一事务内双写、流水只追加；管理员调分走幂等事件，余额在任何操作下恒不为负。

## Elaborates

- techspec: `docs/tech-specs/shop-mall-tech-spec.md` §2、§3；`docs/tech-specs/flows.md` 第 6 节；`docs/tech-specs/data-model.md` 账务域
- techspec: `docs/architecture/07-coupon-pay-lifecycle.md` §3、§4（`order_pay` 事件写入时机移至支付确认，本节 MODIFIED 的依据）

## ADDED Requirements

### Requirement: 余额与流水同事务双写

The system SHALL 保证每一次积分余额变动都对应一条流水，二者在同一数据库事务内生效。

#### Scenario: 任一积分变动

- **WHEN** 注册赠送、下单扣减、退款回分或管理员调整任一事件提交成功
- **THEN** 买家余额与 `points_ledger` 流水同时变化，流水记录的变动后余额与事务提交后的账户余额一致

#### Scenario: 明细页可对账

- **WHEN** 买家查询本人积分明细
- **THEN** 每一笔余额变化都能在流水中找到对应记录，全部流水按类型汇总等于当前余额

#### Scenario: 事务回滚不留单边

- **WHEN** 任一含积分变动的业务事务失败回滚
- **THEN** 余额与流水都不变化，不出现只改余额或只记流水的中间态

### Requirement: 余额恒不为负

The system SHALL 在任何操作下不允许余额扣成负数；扣减超出余额时整个操作失败。

#### Scenario: 扣成负数

- **WHEN** 下单扣减或管理员调减的金额大于当前余额
- **THEN** 请求被拒（下单归 5xxx 段错误码，调整返回业务错误），余额与流水均不变；下单场景的订单、库存、购物车副作用按 `specs/checkout/spec.md` 的原子性约束整体不生效
- **AND** 管理员调减被拒时尽力补记一条失败审计，补记本身失败不改变对调用方的答复

### Requirement: 流水只追加

The system SHALL 不允许修改或删除既有流水；任何修正只能以新增流水完成。

#### Scenario: 无改写通道

- **WHEN** 任何客户端（买家或管理员）尝试变更已写入的流水
- **THEN** 系统不存在修改或删除流水的接口，数据层拒绝此类变更

#### Scenario: 流水字段约束

- **WHEN** 任一流水写入
- **THEN** 金额变动不为 0、变动后余额大于等于 0，类型限 `signup_bonus`、`order_pay`、`order_refund`、`admin_adjust` 四种，且各类型的订单关联、方向、管理员留痕组合符合数据模型约束（如 `order_pay` 必为负值并关联订单、`signup_bonus` 必为正值且无订单关联）

### Requirement: 买家查询本人流水

The system SHALL 向已登录买家分页返回本人余额与全部流水，按时间倒序。

#### Scenario: 查询明细

- **WHEN** 买家 `GET /api/v1/points/ledger`
- **THEN** 返回当前余额与本人流水的分页列表（倒序），每条含类型、变动值、变动后余额、关联订单（如有）与时间

#### Scenario: 未登录

- **WHEN** 无有效买家 JWT（JSON Web Token，服务端签名令牌）请求流水
- **THEN** 响应 401

### Requirement: 管理员幂等调整积分

The system SHALL 让持 `points:adjust` 的超管以有符号增减调整买家余额：幂等键防重复、备注必填、全程留痕。

#### Scenario: 调整成功

- **WHEN** 超管请求 `POST /api/admin/v1/points/adjust`（携带 Idempotency-Key、有符号增减量、备注）
- **THEN** 目标买家余额按增减量变化，写一条 `admin_adjust` 流水，留痕操作管理员与备注
- **AND** 同一事务写入成功审计

#### Scenario: 幂等重放

- **WHEN** 同一管理员以相同 Idempotency-Key 与相同内容重复提交
- **THEN** 返回首次结果，余额不重复变化

#### Scenario: 同键不同内容

- **WHEN** 同一管理员的同一 Idempotency-Key 提交不同增减量或备注
- **THEN** 响应 409，余额与流水不变
- **AND** 尽力补记一条失败审计，补记本身失败不改变对调用方的答复

#### Scenario: 幂等域按管理员隔离

- **WHEN** 两个不同管理员使用同一个 Idempotency-Key 各自提交调整
- **THEN** 两次调整互不冲突、各自生效

#### Scenario: 备注缺失

- **WHEN** 调整请求未带备注
- **THEN** 返回参数错误，余额不变

#### Scenario: 无权限调整

- **WHEN** 管理员角色不含 `points:adjust`（如运营）
- **THEN** 响应 403，且失败审计记录该尝试

### Requirement: 不提供直改余额通道

The system SHALL 不开放任何绕过流水直接设定余额的接口；积分变动只能由注册赠送、下单、退款审批、管理员调整四类事件产生。

#### Scenario: 直接设定余额

- **WHEN** 管理员尝试把某买家余额改为指定值
- **THEN** 只能提交带增减量与备注的调整请求；系统中不存在设定绝对余额的接口

## MODIFIED Requirements

### Requirement: 余额与流水同事务双写

The system SHALL 保证每一次积分余额变动都对应一条流水，二者在同一数据库事务内生效；下单扣减事件改由订单支付确认时产生，下单本身不再触发余额变动。

#### Scenario: 任一积分变动

- **WHEN** 注册赠送、订单确认支付、退款审批回分或管理员调整任一事件提交成功
- **THEN** 买家余额与 `points_ledger` 流水同时变化，流水记录的变动后余额与事务提交后的账户余额一致

### Requirement: 余额恒不为负

The system SHALL 在任何操作下不允许余额扣成负数；扣减超出余额时整个操作失败。

#### Scenario: 扣成负数

- **WHEN** 订单确认支付的扣减额或管理员调减额大于当前余额
- **THEN** 请求被拒（确认支付归 5xxx 段余额不足，调整返回业务错误），余额与流水均不变
- **AND** 确认支付被拒时订单保持待支付、库存与券的预占不变，行为按 `specs/order-payment/spec.md` 约束
- **AND** 管理员调减被拒时尽力补记一条失败审计，补记本身失败不改变对调用方的答复

## Coverage Gaps

- 积分对账任务（发现余额与流水累计不一致后的处置）未定义，沿用 techspec follow-up FU-8e2a5d7c，实现前需先定触发周期与告警形态。
