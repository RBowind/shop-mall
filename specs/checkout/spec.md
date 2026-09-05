# 下单与订单

> 买家从购物车与收货地址提交订单：服务端定价、单一事务内预占、幂等键防重复，成交瞬间固化快照；下单产出待支付订单，支付确认见 `specs/order-payment/spec.md`。

## Elaborates

- techspec: `docs/tech-specs/shop-mall-tech-spec.md` §2、§3、§4；`docs/tech-specs/flows.md` 第 2 节；`docs/tech-specs/interfaces.md` 买家域端点；`docs/tech-specs/data-model.md` 交易域
- techspec: `docs/architecture/07-coupon-pay-lifecycle.md` §2、§4（下单预占与用券，本节 MODIFIED 的依据）

## ADDED Requirements

### Requirement: 收货地址管理

The system SHALL 让买家新增、编辑、删除收货地址，一人至多一个默认地址，每次编辑产生新版本号。

#### Scenario: 新增地址

- **WHEN** 已登录买家 `POST /api/v1/addresses` 提交收件人、电话、地区、详细地址
- **THEN** 创建成功，版本号为 1；第一个地址不自动成为默认

#### Scenario: 设默认顶替旧默认

- **WHEN** 买家把地址 B 设为默认，且已有默认地址 A
- **THEN** 生效后可默认地址只有 B，A 不再是默认

#### Scenario: 并发设默认

- **WHEN** 同一买家同时发起把 B 和把 C 设为默认
- **THEN** 最终默认地址恰有一个，且为 B 或 C 之一

#### Scenario: 编辑递增版本

- **WHEN** 买家编辑已有地址
- **THEN** 版本号递增；版本号由服务端维护，客户端只透传

#### Scenario: 地址列表

- **WHEN** 买家 `GET /api/v1/addresses`
- **THEN** 返回本人全部地址，每条含默认标记与当前版本号

#### Scenario: 删除地址

- **WHEN** 买家 `DELETE /api/v1/addresses/{addressId}` 删除本人地址
- **THEN** 该地址不再出现在列表；历史订单的收货快照不受影响

#### Scenario: 操作非本人地址

- **WHEN** 请求的地址不属于当前买家
- **THEN** 响应 404，他人地址不变

### Requirement: 服务端定价下单

The system SHALL 按服务端实时读取的商品价格与库存完成下单；请求只提交购物车行与地址标识，成交副作用全部在一个数据库事务内完成或全部不发生。

#### Scenario: 下单成功

- **WHEN** 已登录买家 `POST /api/v1/orders`（Header 携带 Idempotency-Key，即客户端生成的防重标识）提交购物车行与地址
- **THEN** 同一事务内完成：扣减库存、扣减积分、创建 `status=paid` 的订单与明细、写一条关联订单的负值 `order_pay` 流水、删除所选购物车行
- **AND** 订单记录服务端生成的成交时间
- **AND** 响应返回订单号与订单内容，状态为 paid

#### Scenario: 请求体不携带价格与身份

- **WHEN** 下单请求体中出现价格、数量上限或 user_id 等多余字段
- **THEN** 服务端一概不采信：身份只取令牌，总价只按服务端读到的商品当前积分价与行数量计算

#### Scenario: 积分不足

- **WHEN** 订单总价大于买家当前余额
- **THEN** 请求失败并返回 5xxx 段余额不足错误码
- **AND** 订单、流水、库存、购物车均不变，幂等键不留存——补分后可用同一键重新下单成功

#### Scenario: 库存不足

- **WHEN** 某行数量大于商品当前库存
- **THEN** 请求失败并返回 3xxx 段库存不足错误码，整体回滚

#### Scenario: 提交含已下架商品的行

- **WHEN** 提交的购物车行对应商品状态为 off_sale
- **THEN** 请求失败并返回 3xxx 段商品下架错误码，整体回滚

#### Scenario: 提交非本人的行或地址

- **WHEN** 购物车行或地址不属于当前买家
- **THEN** 响应 404，无任何副作用

### Requirement: 下单幂等

The system SHALL 以（买家, Idempotency-Key）为幂等域：同键同内容重放首次结果，同键不同内容返回 409。

#### Scenario: 原样重放

- **WHEN** 同一买家以相同 Idempotency-Key 与相同内容再次请求下单（如超时后重试）
- **THEN** 返回首次创建的订单，不产生任何新副作用

#### Scenario: 同键不同内容

- **WHEN** 同一 Idempotency-Key 提交不同的内容
- **THEN** 响应 409 幂等冲突

#### Scenario: 内容一致的判定范围

- **WHEN** 买家改动购物车行、行数量，或地址被编辑导致版本变化
- **THEN** 请求内容视为不同；客户端必须换新键，沿用旧键得到 409

#### Scenario: 并发双击提交

- **WHEN** 同一 Idempotency-Key 的两个首次请求并发到达
- **THEN** 只创建一个订单；另一请求以重放（返回原订单）或 409 结束，不多扣一分积分、不多扣一件库存

### Requirement: 订单快照

The system SHALL 在成交瞬间固化商品与收货信息，历史订单不随商品、地址的后续变化。

#### Scenario: 商品后续变更不影响

- **WHEN** 订单成交后管理员改价、改名、下架该商品，或买家修改收货地址
- **THEN** 订单详情仍返回成交时的商品名、图、单价、数量与收货快照

### Requirement: 买家订单查询

The system SHALL 让买家分页查看本人订单列表与详情，列表支持按状态过滤。

#### Scenario: 按状态过滤

- **WHEN** `GET /api/v1/orders` 带状态参数
- **THEN** 返回本人该状态的订单；五个状态（paid、shipped、completed、refund_requested、refunded）与列表过滤项一一对应

#### Scenario: 列表不带状态参数

- **WHEN** `GET /api/v1/orders` 不带状态参数
- **THEN** 返回本人全部状态的订单（对应"全部"过滤项）

#### Scenario: 订单详情内容

- **WHEN** `GET /api/v1/orders/{orderId}` 请求本人订单
- **THEN** 返回状态、商品快照、收货快照、订单号、各节点时间
- **AND** 退款申请中或已退款的订单含退款原因；曾被驳回的订单含拒绝原因

#### Scenario: 查询他人订单

- **WHEN** orderId 不属于当前买家
- **THEN** 响应 404

## MODIFIED Requirements

### Requirement: 服务端定价下单

The system SHALL 在单一数据库事务内完成下单：预占库存、锁定所选券、删除购物车行，产出待支付订单；积分余额不参与下单，支付确认见 `specs/order-payment/spec.md`。

#### Scenario: 下单成功

- **WHEN** 已登录买家 `POST /api/v1/orders`（Header 携带 Idempotency-Key）提交购物车行、地址，可选携带 `coupon_id`
- **THEN** 同一事务内完成：创建 `pending_payment` 订单与明细快照；订单价=明细快照价合计（原价口径）、券抵扣金额、实付=原价-抵扣三值固化；固化支付截止时间；每个商品可售库存按数量预占（可售减、锁定增）；所选券置 `locked` 并绑定本订单；删除所选购物车行
- **AND** 买家余额与积分流水不发生任何变化
- **AND** 响应返回订单号、三个金额值与支付截止时间，状态为 pending_payment

#### Scenario: 余额不足不再是下单门槛

- **WHEN** 买家余额低于订单原价或实付额时提交下单
- **THEN** 下单照常成功产出待支付订单；能否支付在确认支付时判定

#### Scenario: 券资格服务端判定

- **WHEN** 提交的 `coupon_id` 存在任一不满足：券不属于本买家、状态非 `available`、当前不在模板有效期内、订单原价低于券门槛
- **THEN** 返回 6xxx 段券不可用错误码，订单、库存预占、券、购物车全部不变

#### Scenario: 请求体不携带价格与身份

- **WHEN** 下单请求体中出现价格、数量上限或 user_id 等多余字段
- **THEN** 服务端一概不采信：身份只取令牌，原价只按服务端读到的商品当前积分价与行数量计算，抵扣只按服务端读到的券模板规则计算

#### Scenario: 库存不足

- **WHEN** 某行数量大于商品当前可售库存
- **THEN** 请求失败并返回 3xxx 段库存不足错误码，整体回滚：无订单、无可售扣减与锁定增加、券不锁定、购物车不变

#### Scenario: 提交含已下架商品的行

- **WHEN** 提交的购物车行对应商品状态为 off_sale
- **THEN** 请求失败并返回 3xxx 段商品下架错误码，整体回滚

#### Scenario: 提交非本人的行或地址

- **WHEN** 购物车行或地址不属于当前买家
- **THEN** 响应 404，无任何副作用

### Requirement: 下单幂等

The system SHALL 以（买家, Idempotency-Key）为幂等域：同键同内容重放首次结果，同键不同内容返回 409；内容指纹涵盖购物车行、行数量、地址及其版本、所选券。

#### Scenario: 原样重放

- **WHEN** 同一买家以相同 Idempotency-Key 与相同内容再次请求下单（如超时后重试）
- **THEN** 返回首次创建的订单，不产生任何新副作用

#### Scenario: 同键不同内容

- **WHEN** 同一 Idempotency-Key 提交不同的内容
- **THEN** 响应 409 幂等冲突

#### Scenario: 内容一致的判定范围

- **WHEN** 买家改动购物车行、行数量、所选券，或地址被编辑导致版本变化
- **THEN** 请求内容视为不同；客户端必须换新键，沿用旧键得到 409

#### Scenario: 并发双击提交

- **WHEN** 同一 Idempotency-Key 的两个首次请求并发到达
- **THEN** 只创建一个订单；另一请求以重放（返回原订单）或 409 结束；库存只预占一次、券至多锁定一次，积分与流水不动

### Requirement: 买家订单查询

The system SHALL 让买家分页查看本人订单列表与详情，列表支持按状态过滤，状态集合为七值。

#### Scenario: 按状态过滤

- **WHEN** `GET /api/v1/orders` 带状态参数
- **THEN** 返回本人该状态的订单；七个状态（pending_payment、paid、shipped、completed、refund_requested、refunded、cancelled）与列表过滤项一一对应

#### Scenario: 列表不带状态参数

- **WHEN** `GET /api/v1/orders` 不带状态参数
- **THEN** 返回本人全部状态的订单（对应"全部"过滤项），已取消订单对买家可见

#### Scenario: 订单详情内容

- **WHEN** `GET /api/v1/orders/{orderId}` 请求本人订单
- **THEN** 返回状态、商品快照、收货快照、订单号、原价、券抵扣与实付、各节点时间；待支付订单含支付截止时间，已取消订单含取消时间
- **AND** 用券订单含所用券的名称与抵扣金额
- **AND** 退款申请中或已退款的订单含退款原因；曾被驳回的订单含拒绝原因

#### Scenario: 查询他人订单

- **WHEN** orderId 不属于当前买家
- **THEN** 响应 404

## Coverage Gaps

- 结算页默认地址预选等端内交互归 `docs/architecture/01-miniapp.md`，本 spec 不复述。
- 删除默认地址后允许买家暂无默认地址，还是自动顶替一个，未定义（techspec 只约束"至多一个"）。
- 下单请求体的字段级定义（校验规则、错误码到端点的映射）以 openapi 契约为准；当前 `docs/api/openapi.yaml` 缺失，见 follow-ups。
