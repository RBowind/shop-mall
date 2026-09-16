# Sprint Contract: coupon
Source: specs/coupon/spec.md
Status: DRAFT（待独立 Evaluator 与人 gate）

## Behavioral Changes

- [ ] B1 [ADDED]: 创建券模板 / 创建成功 — verified by BDD test
- [ ] B2 [ADDED]: 创建券模板 / 规则非法 — verified by BDD test
- [ ] B3 [ADDED]: 创建券模板 / 无权限创建 — verified by BDD test
- [ ] B4 [ADDED]: 创建券模板 / 规则创建后不可修改 — verified by BDD test
- [ ] B5 [ADDED]: 切换发放状态 / 停发 — verified by BDD test
- [ ] B6 [ADDED]: 切换发放状态 / 恢复发放 — verified by BDD test
- [ ] B7 [ADDED]: 切换发放状态 / 切换留痕 — verified by BDD test
- [ ] B8 [ADDED]: 管理员查看模板 / 模板列表 — verified by BDD test
- [ ] B9 [ADDED]: 管理员查看模板 / 无权限查看 — verified by BDD test
- [ ] B10 [ADDED]: 领券中心可见性 / 列表内容 — verified by BDD test
- [ ] B11 [ADDED]: 领券中心可见性 / 不可领模板不展示 — verified by BDD test
- [ ] B12 [ADDED]: 领取优惠券 / 领取成功 — verified by BDD test
- [ ] B13 [ADDED]: 领取优惠券 / 达到每人限领 — verified by BDD test
- [ ] B14 [ADDED]: 领取优惠券 / 模板售罄与并发不超发 — verified by BDD test
- [ ] B15 [ADDED]: 领取优惠券 / 模板不可领 — verified by BDD test
- [ ] B16 [ADDED]: 领取优惠券 / 幂等重放 — verified by BDD test
- [ ] B17 [ADDED]: 领取优惠券 / 并发双击领取 — verified by BDD test
- [ ] B18 [ADDED]: 券生命周期状态机 / 下单占用与释放后可复用 — verified by BDD test
- [ ] B19 [ADDED]: 券生命周期状态机 / 一张券至多被一笔在途订单占用 — verified by BDD test
- [ ] B20 [ADDED]: 券生命周期状态机 / 过期扫描只动未占用的券 — verified by BDD test
- [ ] B21 [ADDED]: 券生命周期状态机 / 已过期券不再被任何路径复用 — verified by BDD test
- [ ] B22 [ADDED]: 买家查询我的券 / 按状态查券 — verified by BDD test
- [ ] B23 [ADDED]: 买家查询我的券 / 只返回本人的券 — verified by BDD test

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
- 状态取 `available`、`locked`、`used`、`expired`；索引保证领取幂等与一券至多被一笔订单占用。

### permissions
- 新增 `coupon:read` 与 `coupon:write` 权限码；模板创建与发放状态切换写审计。

## Follow-ups
- 模板已核销数的大数据量展示口径待定。
- 模板数量与每人限领的字段级上限以 OpenAPI 契约为准。

## Pass Rule
ALL Behavioral Changes + ALL Quality + lint/test 绿。
