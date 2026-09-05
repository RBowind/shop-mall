# Sprint Contract: refund
Source: specs/refund/spec.md
Status: APPROVED（人 gate 已过，2026-09-01；2026-09-02 由 devloop 启动时经用户确认统一改标记）

## Behavioral（from spec scenarios）
- [ ] B1: 申请成功——paid→refund_requested，原因与申请时间留存可见 — verified by BDD test
- [ ] B2: 已发货订单申请——409 状态不变（收紧现实现：后端当前放行 shipped，需按本契约改守卫） — verified by BDD test
- [ ] B3: 其他状态申请——409 — verified by BDD test
- [ ] B4: 申请他人订单退款——404 — verified by BDD test
- [ ] B5: 审批通过——同事务退积分+order_refund 流水+回库存+refunded+审核留痕+成功审计 — verified by BDD test
- [ ] B6: 重复或并发审批——409 不重复退分回库存 — verified by BDD test
- [ ] B7: 无审批权限——403+失败审计 — verified by BDD test
- [ ] B8: 驳回成功——回 paid+原因留痕，无账务库存副作用 — verified by BDD test
- [ ] B9: 缺少拒绝原因——1xxx 参数错误订单不变 — verified by BDD test
- [ ] B10: 重复驳回——409 状态原因留痕均不变 — verified by BDD test
- [ ] B11: 驳回后再次申请——正常进入 refund_requested — verified by BDD test
- [ ] B12: 查看列表——refund_requested 与 refunded 含原因 — verified by BDD test
- [ ] B13: 无查看权限——403 — verified by BDD test

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 审批原子性测试（事务中途失败 → 积分、库存、状态、流水全部不半边生效）
- [ ] Q3: 并发审批安全用例（两请求只一个成功，另一个 409 无副作用）
- [ ] Q4: 金额口径测试（退款额=订单快照总额，审批请求体带金额字段也不采信）
- [ ] Q5: shipped 申请 409 的回归测试（B2 即守卫收紧的验收锚点）

## Schema Changes
### orders
- `refund_requested_at` TIMESTAMPTZ NULL - 申请退款时服务端写入，供买家侧展示申请时间；存量行不回填。迁移只向前（goose 无 down）。

## Follow-ups
- FU-4e7c1a52: 买家退款原因服务端是否强制非空及长度上限，待产品口径。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿 + refund_requested_at 迁移通过 `make contract-check` 与迁移构建检查（N-008）。
