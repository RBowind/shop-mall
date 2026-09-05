# 优惠券

> 管理员创建满减券模板，买家自助领取；券随下单、支付、取消、退款流转，过期由后台扫描置终态。所有判额与判期都在服务端。

## Elaborates

- techspec: `docs/architecture/07-coupon-pay-lifecycle.md` §2、§3、§5；主档 `docs/tech-specs/interfaces.md`（错误码段与鉴权约定）

## ADDED Requirements

### Requirement: 创建券模板

The system SHALL 让持 `coupon:write` 的管理员创建满减券模板：门槛、抵扣、发放总量、每人限领、有效期起止；创建即冻结，之后规则不可改。

#### Scenario: 创建成功

- **WHEN** 持 `coupon:write` 的管理员 `POST /api/admin/v1/coupon-templates` 提交名称、门槛积分、抵扣积分、总量、每人限领、`valid_from` 与 `valid_until`
- **THEN** 模板创建成功，初始状态为发放中（`active`），已领取数为 0
- **AND** 一条成功审计记录本次创建

#### Scenario: 规则非法

- **WHEN** 提交不满足任一规则约束：门槛或抵扣不是正整数、抵扣不小于门槛、总量或每人限领不是正整数、`valid_until` 不晚于 `valid_from`
- **THEN** 返回 1xxx 段参数错误，不创建模板，已创建模板不受影响

#### Scenario: 无权限创建

- **WHEN** 管理员角色不含 `coupon:write`
- **THEN** 响应 403，且一条失败审计记录该尝试

#### Scenario: 规则创建后不可修改

- **WHEN** 对已有模板提交规则字段（门槛、抵扣、总量、限领、有效期）的修改请求
- **THEN** 请求中仅发放状态变更生效，规则字段一概不被采信

### Requirement: 切换发放状态

The system SHALL 允许管理员在发放中（`active`）与停发（`halted`）之间切换模板；停发只影响后续领取，不影响已领券。

#### Scenario: 停发

- **WHEN** 管理员把 `active` 模板切为 `halted`
- **THEN** 该模板不再出现在领券中心，对其领取请求被拒
- **AND** 已领到的券的占用、核销、退回、过期处置全部照常

#### Scenario: 恢复发放

- **WHEN** `halted` 模板切回 `active` 且当前时间处于其有效期内
- **THEN** 模板重新出现在领券中心并可被领取

#### Scenario: 切换留痕

- **WHEN** 任一发放状态切换成功
- **THEN** 一条成功审计记录操作人与切换前后状态

### Requirement: 管理员查看模板

The system SHALL 向持 `coupon:read` 的管理员分页返回全部券模板，含发放进度与核销数。

#### Scenario: 模板列表

- **WHEN** 管理员 `GET /api/admin/v1/coupon-templates`
- **THEN** 返回含 `halted` 在内的全部模板，每条带规则字段、已领取数、已核销数（该模板下状态为 `used` 的券的计数）

#### Scenario: 无权限查看

- **WHEN** 管理员角色不含 `coupon:read`
- **THEN** 响应 403

### Requirement: 领券中心可见性

The system SHALL 向已登录买家展示正在发放且当前处于有效期内的模板，附本人已领数。

#### Scenario: 列表内容

- **WHEN** 买家 `GET /api/v1/coupons/center`
- **THEN** 返回 `status` 为 `active` 且 `valid_from` 已过、`valid_until` 未到的模板，分页；每条附当前买家在该模板下的已领数与可领标记（未达限领且未售罄为可领）

#### Scenario: 不可领模板不展示

- **WHEN** 模板为 `halted`、未到 `valid_from` 或已过 `valid_until`
- **THEN** 该模板不出现在领券中心列表

### Requirement: 领取优惠券

The system SHALL 让买家以 Idempotency-Key 领取一张券：受模板总量与每人限领双重约束，同键重复提交不重复发券，并发下不超发。

#### Scenario: 领取成功

- **WHEN** 买家 `POST /api/v1/coupons/{templateId}/receive` 携带 Idempotency-Key（幂等键，HTTP Header，UUID 文本，库上落为 `user_coupons.request_id`），模板可领且本人未达限领、模板未售罄
- **THEN** 买家获得一张可用（`available`）券，模板已领取数加一

#### Scenario: 达到每人限领

- **WHEN** 买家在该模板下已持有的券数达到 `per_user_limit`
- **THEN** 返回 6xxx 段已达限领错误码，券数与已领取数不变

#### Scenario: 模板售罄与并发不超发

- **WHEN** 剩余可领数少于同时发起的领取请求数
- **THEN** 恰好发出剩余数量的券，其后请求全部返回 6xxx 已领完；已领取数从不超过模板总量

#### Scenario: 模板不可领

- **WHEN** 模板不存在、为 `halted`、未到 `valid_from` 或已过 `valid_until`
- **THEN** 返回 6xxx 段券不可领取错误码，无任何副作用

#### Scenario: 幂等重放

- **WHEN** 同一买家以相同 Idempotency-Key 与相同模板重复提交（如超时重试）
- **THEN** 返回首次领取结果，不再多发一张
- **AND** 同一键绑定了不同模板时返回 409，不领券

#### Scenario: 并发双击领取

- **WHEN** 同键两个首次请求并发到达
- **THEN** 只发一张券；另一请求以重放结束；换一个键的请求视为新领取，正常消耗限领数与总量

### Requirement: 券生命周期状态机

The system SHALL 使券只沿以下路径流转：领取为可用（`available`）；被下单占用为锁定（`locked`）；订单支付成功为已核销（`used`）；订单取消或退款审批通过时，未过期回到可用、已过期置过期（`expired`）；未被占用的券过有效期由扫描置过期。除此之外不发生状态变化。

#### Scenario: 下单占用与释放后可复用

- **WHEN** 一张可用券随下单被锁定，随后该订单超时取消且券未过有效期
- **THEN** 券回到可用，可被该买家的新订单再次占用；两张订单不同时占用同一张券
- **AND** 取消时券已过有效期则置为过期，不再可用

#### Scenario: 一张券至多被一笔在途订单占用

- **WHEN** 买家把同一张券先后提交给两笔下单请求（第一笔已锁定该券）
- **THEN** 第二笔请求返回 6xxx 段券不可用错误码

#### Scenario: 过期扫描只动未占用的券

- **WHEN** 周期扫描运行时券为 `available` 且模板 `valid_until` 已过
- **THEN** 券置为 `expired`，不出现在可用券列表、下单不再接受
- **AND** 处于 `locked` 或 `used` 的券不因扫描改状态

#### Scenario: 已过期券不再被任何路径复用

- **WHEN** 后续轮次的过期扫描、退款审批或其他任何操作触达一张 `expired` 的券
- **THEN** 该券保持 `expired`，不存在使其回到可用的路径

### Requirement: 买家查询我的券

The system SHALL 向买家分页返回本人券，支持按状态过滤，响应含券规则与占用订单。

#### Scenario: 按状态查券

- **WHEN** 买家 `GET /api/v1/me/coupons` 带 `status` 参数（`available`、`locked`、`used`、`expired` 其一）
- **THEN** 返回本人该状态的券，每条含券名、门槛、抵扣、`valid_until`、状态；`locked` 与 `used` 的券含占用它的订单号

#### Scenario: 只返回本人的券

- **WHEN** 买家查询
- **THEN** 结果不含任何其他买家的券；券按用户归属由令牌身份界定，无按他人券 ID 操作的接口路径

## Coverage Gaps

- 模板列表"已核销数"的实时聚合口径（大总量下是否改物化计数）未定义，当前按聚合查询实现，数据量上来后再定。
- 券模板数量、每人限领的字段级上限（如总量最大值）未定义，以入库后的 openapi 契约为准。
- 领券中心与我的券页面交互归 `docs/architecture/01-miniapp.md`，本 spec 不复述。
