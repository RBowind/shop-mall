# Sprint Contract: points
Source: specs/points/spec.md
Status: APPROVED（人 gate 已过，2026-09-01；2026-09-02 由 devloop 启动时经用户确认统一改标记）

## Behavioral（from spec scenarios）
- [ ] B1: 任一积分变动——余额与流水同事务、balance_after 一致 — verified by BDD test
- [ ] B2: 明细页可对账——流水累计等于余额 — verified by BDD test
- [ ] B3: 事务回滚不留单边——无只改余额或只记流水的中间态 — verified by BDD test
- [ ] B4: 扣成负数——被拒余额流水不变，调减被拒尽力补记失败审计 — verified by BDD test
- [ ] B5: 无改写通道——不存在修改/删除流水的接口 — verified by BDD test
- [ ] B6: 流水字段约束——delta≠0、balance_after≥0、四类型联动组合 — verified by BDD test
- [ ] B7: 查询明细——本人倒序分页含关联订单 — verified by BDD test
- [ ] B8: 未登录——401 — verified by BDD test
- [ ] B9: 调整成功——余额变化+admin_adjust 流水+成功审计同事务 — verified by BDD test
- [ ] B10: 幂等重放——同键同内容不重复到账 — verified by BDD test
- [ ] B11: 同键不同内容——409 且尽力补记失败审计 — verified by BDD test
- [ ] B12: 幂等域按管理员隔离——两管理员同键各自生效 — verified by BDD test
- [ ] B13: 备注缺失——参数错误余额不变 — verified by BDD test
- [ ] B14: 无权限调整——403+失败审计 — verified by BDD test
- [ ] B15: 直接设定余额——无此通道 — verified by BDD test

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 不变量测试——尝试把余额扣成负数、写 delta=0 流水、改写/删除流水，全部被数据库约束拒绝
- [ ] Q3: 调分幂等安全用例（B10–B12 有专用例）
- [ ] Q4: 对账断言——集成测试后流水按序累计与 users.points_balance 恒等

## Schema Changes
无新增（points_ledger 现有结构满足）。

## Follow-ups
- FU-8e2a5d7c（沿用 techspec）: 积分对账任务的触发周期、告警形态、冻结口径需先定义再实现。
- FU-9d3e6a28: 超管调分后同页查流水核对的小程序/后台前端未接线（PRD §8-2，ADMIN_TARGET 既定未完成项）。
- FU-3d7b8c1a（沿用 techspec）: `GET /api/admin/v1/users`、`GET /api/admin/v1/audit-logs` 补入 openapi.yaml（前提同为 FU-a1f4c902）。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
