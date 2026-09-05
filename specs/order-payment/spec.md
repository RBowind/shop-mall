# 订单支付与超时取消

> 下单产出待支付订单并预占库存与券；买家在支付时限内确认支付扣积分成交；超时未付由后台任务自动取消并释放占用。

## Elaborates

- techspec: `docs/architecture/07-coupon-pay-lifecycle.md` §2、§4；`docs/tech-specs/flows.md` 全局事务规则
- 关联 capability: 下单产出待支付订单见 `specs/checkout/spec.md`，订单终态后的发货收货见 `specs/fulfillment/spec.md`
- 后台周期任务的装配模式（ticker + ctx 退出可停止）以 `backend/cmd/server` 的图片清理循环为同源；本 spec 内的"周期任务"特指该任务模板下的超时取消与过期券清理两段共享节奏的同轮扫描。

## ADDED Requirements

### Requirement: 确认支付

The system SHALL 只允许买家本人把待支付（`pending_payment`）订单按实付积分一次性迁移为已支付（`paid`），成交副作用全部在同一数据库事务内。

#### Scenario: 支付成功

- **WHEN** 买家 `POST /api/v1/orders/{orderId}/pay` 请求支付本人 `pending_payment` 订单，余额不小于实付积分
- **THEN** 同一事务内：订单状态变 `paid` 并记支付时间；买家余额按 `pay_points`（实付=原价-券抵扣）扣减；写一条关联该订单的负值 `order_pay` 流水；该订单预占的锁定库存消耗掉（锁定数减、可售数不回增）；占用的券若存在则核销为 `used`
- **AND** 响应返回已支付订单

#### Scenario: 余额不足

- **WHEN** 余额小于订单实付积分
- **THEN** 返回 5xxx 段余额不足错误码；订单保持 `pending_payment`，库存与券的预占不变，余额、流水均不动；管理员补分后同一订单可再次支付成功

#### Scenario: 非待支付状态支付

- **WHEN** 订单已为 `paid`、`cancelled`、`shipped`、`completed`、`refund_requested` 或 `refunded`
- **THEN** 响应 409，不重复扣积分、不重复记流水；买家查询订单即可获知当前状态

#### Scenario: 并发与重复支付只成交一次

- **WHEN** 同一订单的两个支付请求并发到达，或支付成功后再次提交支付
- **THEN** 只扣一次积分、只写一条 `order_pay` 流水、只核销一次券；其余请求全部 409

#### Scenario: 支付他人订单

- **WHEN** orderId 不属于当前买家
- **THEN** 响应 404，订单与账务不变

### Requirement: 支付时限

The system SHALL 在下单成功时为订单固化支付截止时间并随订单返回；确认支付只在该时限内完成的请求与后台取消的竞速中至多一方成功。

#### Scenario: 时限随订单生成

- **WHEN** 下单成功
- **THEN** 订单响应与详情包含支付截止时间，取值为下单时刻加支付时限（业务 SLA 默认 15 分钟，由 `ORDER_PAY_TIMEOUT_MINUTES` 配置项设定）

#### Scenario: 取消处理前仍可支付

- **WHEN** 订单已过支付截止时间但后台取消尚未处理到它，买家发起支付
- **THEN** 支付成功按"确认支付"场景生效，该订单不再被后台取消

### Requirement: 超时自动取消

The system SHALL 由周期任务取消所有已超过支付截止时间且仍待支付的订单：释放预占库存、按有效期处置券、记录取消时间；不回补购物车，不产生任何积分变动。

#### Scenario: 超时订单被取消

- **WHEN** 周期扫描时订单为 `pending_payment` 且已到达支付截止时间
- **THEN** 订单状态变 `cancelled` 并记取消时间；订单内每个商品的可售库存按数量回补、锁定库存相应减少；占用的券未过期回到可用、已过期置过期
- **AND** 买家余额与积分流水不变；购物车不因取消发生变化

#### Scenario: 取消与支付并发只有一方生效

- **WHEN** 后台取消与买家支付同时处理同一笔到期订单
- **THEN** 订单终态恰为 `paid` 或 `cancelled` 之一；支付赢则库存不回补、券为 `used`，取消赢则支付请求返回 409 且库存与券已释放

#### Scenario: 同一订单重复处理只释放一次

- **WHEN** 同一到期订单被相邻两轮扫描或两个任务实例同时处理
- **THEN** 可售库存只回补一次、券只处置一次；不存在双倍回补

#### Scenario: 处理失败不漏单

- **WHEN** 某订单本轮取消中途失败（事务回滚）
- **THEN** 订单保持 `pending_payment`，后续轮次按同样条件再次命中并完成取消；其他订单的处理不受该单失败影响

#### Scenario: 取消是终态

- **WHEN** 订单已为 `cancelled`
- **THEN** 支付、发货、申请退款对其一律拒绝（409），库存与券不再因它变动

### Requirement: 过期券清理任务

The system SHALL 由后台周期任务把已过有效期且未被占用的券置为过期；券的状态转移路径以 `specs/coupon/spec.md` 状态机为唯一出处。

#### Scenario: 过期券被置终态

- **WHEN** 周期任务运行且券为 `available`、模板 `valid_until` 已过
- **THEN** 券置为 `expired`；重复运行不产生额外变化，也不产生积分、订单或通知层面的副作用

## Coverage Gaps

- 系统不提供买家主动取消待支付订单的入口（无 `DELETE` 或取消类端点）：买家要么在支付时限内确认支付，要么等待后台任务自动取消；本 spec 范围内不新增此类接口。
- 超时取消与过期券清理由同一周期任务在同轮内先后扫描完成（一轮先扫到期订单再扫过期券），具体扫描周期与每轮批量大小以配置项为准；spec 只约束"到期后有限轮内必被处理、失败可重入"。
- 取消与核销对买家通知（站内展示或订阅消息）未定义：PRD 明确本期不做消息通知，取消结果以订单列表状态为准。
- 支付接口是否需要独立错误码细分（超时竞态 vs 已支付）未定义，两者当前都归 409 状态迁移非法。
