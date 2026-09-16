# Sprint Contract: points
Source: specs/points/spec.md
Status: APPROVED

## Behavioral Changes

- [ ] B1 [ADDED]: 流水只追加 / 无改写通道
- [ ] B2 [ADDED]: 流水只追加 / 流水字段约束
- [ ] B3 [ADDED]: 买家查询本人流水 / 查询明细
- [ ] B4 [ADDED]: 买家查询本人流水 / 未登录
- [ ] B5 [ADDED]: 管理员幂等调整积分 / 调整成功
- [ ] B6 [ADDED]: 管理员幂等调整积分 / 幂等重放
- [ ] B7 [ADDED]: 管理员幂等调整积分 / 同键不同内容
- [ ] B8 [ADDED]: 管理员幂等调整积分 / 幂等域按管理员隔离
- [ ] B9 [ADDED]: 管理员幂等调整积分 / 备注缺失
- [ ] B10 [ADDED]: 管理员幂等调整积分 / 无权限调整
- [ ] B11 [ADDED]: 不提供直改余额通道 / 直接设定余额
- [ ] B12 [MODIFIED]: 余额与流水保持一致 / 任一积分变动
- [ ] B13 [MODIFIED]: 余额恒不为负 / 扣成负数

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

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
