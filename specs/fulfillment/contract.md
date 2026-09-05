# Sprint Contract: fulfillment
Source: specs/fulfillment/spec.md
Status: APPROVED（人 gate 已过，2026-09-01；2026-09-02 由 devloop 启动时经用户确认统一改标记）

## Behavioral（from spec scenarios）
- [ ] B1: 发货成功——paid→shipped，发货人取认证身份+成功审计 — verified by BDD test
- [ ] B2: 状态不符——非 paid 发货 409，操作人时间不改写 — verified by BDD test
- [ ] B3: 订单不存在——404 — verified by BDD test
- [ ] B4: 无权限管理员——403+失败审计 — verified by BDD test
- [ ] B5: 确认成功——shipped→completed+完成时间 — verified by BDD test
- [ ] B6: 其他状态确认——409 — verified by BDD test
- [ ] B7: 重复确认——409 不重写完成时间 — verified by BDD test
- [ ] B8: 确认他人订单——404 — verified by BDD test
- [ ] B9: 列表查询——全量+脱敏+分页 — verified by BDD test
- [ ] B10: 详情查询——含脱敏收货信息与各节点时间 — verified by BDD test
- [ ] B11: 无权限查询——403 — verified by BDD test
- [ ] B12: 查询不存在的订单——404 — verified by BDD test

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 状态机守卫测试——四种非法源状态发货/三种非法状态确认均 409 且无副作用
- [ ] Q3: 审计用例——发货成功与 403 失败各产生 result=success/failure 条目
- [ ] Q4: 脱敏测试——管理端订单列表/详情响应不含明文手机号与详细地址

## Schema Changes
无新增（orders 状态与时间列现有结构满足）。

## Follow-ups
- FU-1c6f9b3e（沿用 techspec）: 脱敏具体格式（手机号保留位）待产品拍板，Q4 当前只断言"非明文"。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
