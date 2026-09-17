# Sprint Contract: coupon
Source: specs/coupon/spec.md
Status: APPROVED

## Behavioral Changes

- [ ] B1 [ADDED]: 创建券模板 / 创建成功
- [ ] B2 [ADDED]: 创建券模板 / 规则非法
- [ ] B3 [ADDED]: 创建券模板 / 无权限创建
- [ ] B4 [ADDED]: 创建券模板 / 规则创建后不可修改
- [ ] B5 [ADDED]: 切换发放状态 / 停发
- [ ] B6 [ADDED]: 切换发放状态 / 恢复发放
- [ ] B7 [ADDED]: 切换发放状态 / 切换留痕
- [ ] B8 [ADDED]: 管理员查看模板 / 模板列表
- [ ] B9 [ADDED]: 管理员查看模板 / 无权限查看
- [ ] B10 [ADDED]: 领券中心可见性 / 列表内容
- [ ] B11 [ADDED]: 领券中心可见性 / 不可领模板不展示
- [ ] B12 [ADDED]: 领取优惠券 / 领取成功
- [ ] B13 [ADDED]: 领取优惠券 / 达到每人限领（未过期的可用、被预占、已核销三者之和达 `per_user_limit`）
- [ ] B14 [ADDED]: 领取优惠券 / 模板售罄与并发不超发
- [ ] B15 [ADDED]: 领取优惠券 / 模板不可领
- [ ] B16 [ADDED]: 领取优惠券 / 幂等重放
- [ ] B17 [ADDED]: 领取优惠券 / 并发双击领取
- [ ] B18 [ADDED]: 券生命周期状态机 / 下单占用与释放后可复用
- [ ] B19 [ADDED]: 券生命周期状态机 / 一张券至多被一笔在途订单占用
- [ ] B20 [ADDED]: 券生命周期状态机 / 过期扫描只动未占用的券
- [ ] B21 [ADDED]: 券生命周期状态机 / 已过期券不再被任何路径复用
- [ ] B22 [ADDED]: 买家查询我的券 / 按状态查券
- [ ] B23 [ADDED]: 买家查询我的券 / 只返回本人的券
- [ ] B24 [ADDED]: 领取优惠券 / 过期券让出额度

## Quality
- [ ] Q1: 后端全量测试与优惠券行为测试绿
- [ ] Q2: 并发领取不超发，重复领取不重复发券
- [ ] Q3: 券状态迁移、归属校验与幂等行为覆盖
- [ ] Q4: 模板创建、停发和查询的权限与审计覆盖

## Schema Changes

### coupon_templates
- `id` UUID NOT NULL — 主键
- `name`, `threshold_points`, `discount_points`, `total_count`, `per_user_limit`, `valid_from`, `valid_until` — 模板规则字段
- `received_count` INT NOT NULL DEFAULT 0 — 已领取数量
- `status` VARCHAR(16) NOT NULL — `active` 或 `halted`

### user_coupons
- `id` UUID NOT NULL — 主键
- `user_id`, `template_id`, `status`, `order_id`, `request_id` — 券归属、生命周期与领取幂等字段
- 状态取 `available`、`held`、`used`、`expired`；索引保证领取幂等与一券至多被一笔订单占用。

### permissions
- 新增 `coupon:read` 与 `coupon:write` 权限码；模板创建与发放状态切换写审计。

## Follow-ups
- FU-4d12b0e8: 模板列表"已核销数"在大总量下是否改物化计数未定义。
- FU-0a8e7f3b: 券模板数量与每人限领的字段级上限无来源定义（`docs/api/openapi.yaml` 无任何券路径，券端点只定义在 `docs/architecture/07-coupon-pay-lifecycle.md` §5）；上限口径待定，券端点补入 openapi 另立任务。

## Pass Rule
ALL Behavioral Changes + ALL Quality + lint/test 绿。
