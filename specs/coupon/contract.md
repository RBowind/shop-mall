# Sprint Contract: coupon
Source: specs/coupon/spec.md
Status: APPROVED

## Behavioral Changes

- [x] B1 [ADDED]: 创建券模板 / 创建成功
  - 测试驱动开发（Test-Driven Development，TDD）红绿证据：`cd backend && go test ./tests/e2e/ -run TestSpecCouponB1 -count=1` 红 → `coupon_spec_test.go:68: POST /api/admin/v1/coupon-templates status = 404 body=404 page not found`；绿 → `ok shop-mall/backend/tests/e2e 2.807s` | 受影响包：`./internal/coupon/... ./internal/platform/database/... ./cmd/server/... ./tests/...` 全 ok，e2e 全量 30 条 PASS（既有 29 + B1），`gofmt -l .` 空
- [x] B2 [ADDED]: 创建券模板 / 规则非法
  - 测试驱动开发（Test-Driven Development，TDD）红绿证据：`cd backend && go test ./tests/e2e/ -run TestSpecCouponB2 -count=1` 红 → 12 个子用例全部 `status=500 code=5001`（非法输入穿透到 INSERT，被 0006 的 CHECK 以 `SQLSTATE 23514` 拦下，模板行未落库）；绿 → `ok shop-mall/backend/tests/e2e 2.797s` | 受影响包：`./internal/coupon/... ./internal/platform/http/...` ok，e2e 全量 31 条 PASS，`gofmt -l .` 空。校验落在 service 层，失败早于任何写操作
- [x] B3 [ADDED]: 创建券模板 / 无权限创建
  - 测试驱动开发（Test-Driven Development，TDD）红绿证据：`cd backend && go test ./tests/e2e/ -run TestSpecCouponB3 -count=1` 红 → 403 断言已过、失败在失败审计行数 0（`audit_logs` 中 `result='failure'` 恰一条未满足）；绿 → `ok shop-mall/backend/tests/e2e 2.861s` | 受影响包：`./internal/middleware/... ./tests/security/...` ok（安全套件无回归），e2e 全量 32 条 PASS，`gofmt -l .` 空。新增共享闸门 `middleware.AuditedAdminPermission`，仅券模板创建路由启用
- [x] B4 [ADDED]: 创建券模板 / 规则创建后不可修改
  - 测试驱动开发（Test-Driven Development，TDD）红绿证据：`cd backend && go test ./tests/e2e/ -run TestSpecCouponB4 -count=1` 红 → 两轮子测试，轮 1「带发放状态字段」`PATCH /api/admin/v1/coupon-templates/{id} status=404 body=404 page not found`（承重断言是"该请求必须被受理"，因为规则字段不被采信要在受理前提下才有意义）；绿 → `ok shop-mall/backend/tests/e2e 2.739s` | 受影响包：`./internal/coupon/... ./internal/middleware/... ./tests/security/...` ok，e2e 全量 33 条 PASS，`gofmt -l .` 空。请求体白名单只声明 `status`，service 侧单列 UPDATE；切换审计留给 B7
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

## 执行约定
- 循环内只跑当前行为与受影响包；全量 `go test ./...` 留到本 contract 全部行为交付后统一跑一次。理由：本仓 `./internal/...` 单次全量运行在十分钟量级（个别包单跑近三分钟），逐条行为跑全量会把循环拖垮。

## 变异候选（交付前供变异轮核对，非断言）
- 「门槛为正整数」这条服务层校验与「抵扣为正整数」在数学上不可分离：`discount_points > 0` 且 `discount_points < threshold_points` 同时成立即推出 `threshold_points > 0`，任何黑盒测试都无法区分。变异轮把它记为等价变异体候选，不要为它补测。
- 「门槛为负」那条子用例的输入（`threshold=-500, discount=-1000`）满足 `discount < threshold`，真正决定性的违反是 `discount > 0`，判别力上等同「抵扣为负」。
