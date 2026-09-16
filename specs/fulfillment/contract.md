# Sprint Contract: fulfillment
Source: specs/fulfillment/spec.md
Status: DRAFT（待独立 Evaluator 与人 gate）

## Behavioral Changes

- [ ] B1 [MODIFIED]: 管理员发货 / 发货成功
- [ ] B2 [MODIFIED]: 管理员发货 / 状态不符
- [ ] B3 [MODIFIED]: 管理员发货 / 订单不存在
- [ ] B4 [MODIFIED]: 管理员发货 / 无权限管理员
- [ ] B5 [MODIFIED]: 买家确认收货 / 确认成功
- [ ] B6 [MODIFIED]: 买家确认收货 / 其他状态确认
- [ ] B7 [MODIFIED]: 买家确认收货 / 重复确认
- [ ] B8 [MODIFIED]: 买家确认收货 / 确认他人订单
- [ ] B9 [MODIFIED]: 管理端订单查询 / 列表查询
- [ ] B10 [MODIFIED]: 管理端订单查询 / 详情查询
- [ ] B11 [MODIFIED]: 管理端订单查询 / 无权限查询
- [ ] B12 [MODIFIED]: 管理端订单查询 / 查询不存在的订单

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
