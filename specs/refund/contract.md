# Sprint Contract: refund
Source: specs/refund/spec.md
Status: DRAFT（待独立 Evaluator 与人 gate）

## Behavioral Changes

- [ ] B1 [MODIFIED]: 申请退款仅限未发货订单 / 申请成功
- [ ] B2 [MODIFIED]: 申请退款仅限未发货订单 / 缺少退款原因
- [ ] B3 [MODIFIED]: 申请退款仅限未发货订单 / 已发货订单申请
- [ ] B4 [MODIFIED]: 申请退款仅限未发货订单 / 其他状态申请（含 pending_payment/cancelled）
- [ ] B5 [MODIFIED]: 申请退款仅限未发货订单 / 申请他人订单退款
- [ ] B6 [MODIFIED]: 审批通过退还积分并回补库存 / 审批通过（金额=实付、退券副作用）
- [ ] B7 [MODIFIED]: 审批通过退还积分并回补库存 / 重复或并发审批
- [ ] B8 [MODIFIED]: 审批通过退还积分并回补库存 / 无审批权限
- [ ] B9 [ADDED]: 驳回仅回退状态 / 驳回成功
- [ ] B10 [ADDED]: 驳回仅回退状态 / 缺少拒绝原因
- [ ] B11 [ADDED]: 驳回仅回退状态 / 重复驳回
- [ ] B12 [ADDED]: 驳回仅回退状态 / 驳回后再次申请
- [ ] B13 [ADDED]: 退款列表查看 / 查看列表
- [ ] B14 [ADDED]: 退款列表查看 / 无查看权限

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 审批原子性测试（事务中途失败 → 积分、库存、状态、流水全部不半边生效）
- [ ] Q3: 并发审批安全用例（两请求只一个成功，另一个 409 无副作用）
- [ ] Q4: 金额口径测试（退款额=订单快照实付（原价-券抵扣），审批请求体带金额字段也不采信）
- [ ] Q5: shipped 申请 409 的回归测试（B3 即守卫收紧的验收锚点）

## Schema Changes
### orders
- `refund_requested_at` TIMESTAMPTZ NULL - 申请退款时服务端写入，供买家侧展示申请时间；存量行不回填。迁移只向前（goose 无 down）。

## Follow-ups
- FU-4e7c1a52: 买家退款原因服务端是否强制非空及长度上限，待产品口径。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿 + refund_requested_at 迁移通过 `make contract-check` 与迁移构建检查（N-008）。
