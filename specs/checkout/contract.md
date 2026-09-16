# Sprint Contract: checkout
Source: specs/checkout/spec.md
Status: DRAFT（待独立 Evaluator 与人 gate）

## Behavioral Changes

- [ ] B1 [ADDED]: 收货地址管理 / 新增地址
- [ ] B2 [ADDED]: 收货地址管理 / 设默认顶替旧默认
- [ ] B3 [ADDED]: 收货地址管理 / 并发设默认
- [ ] B4 [ADDED]: 收货地址管理 / 编辑递增版本
- [ ] B5 [ADDED]: 收货地址管理 / 地址列表
- [ ] B6 [ADDED]: 收货地址管理 / 删除地址
- [ ] B7 [ADDED]: 收货地址管理 / 操作非本人地址
- [ ] B8 [ADDED]: 订单快照 / 商品后续变更不影响
- [ ] B9 [MODIFIED]: 服务端定价下单 / 下单成功
- [ ] B10 [MODIFIED]: 服务端定价下单 / 余额不足不再是下单门槛
- [ ] B11 [MODIFIED]: 服务端定价下单 / 券资格服务端判定
- [ ] B12 [MODIFIED]: 服务端定价下单 / 请求体不携带价格与身份
- [ ] B13 [MODIFIED]: 服务端定价下单 / 库存不足
- [ ] B14 [MODIFIED]: 服务端定价下单 / 提交含已下架商品的行
- [ ] B15 [MODIFIED]: 服务端定价下单 / 提交非本人的行或地址
- [ ] B16 [MODIFIED]: 下单幂等 / 原样重放
- [ ] B17 [MODIFIED]: 下单幂等 / 同键不同内容
- [ ] B18 [MODIFIED]: 下单幂等 / 内容一致的判定范围
- [ ] B19 [MODIFIED]: 下单幂等 / 并发双击提交
- [ ] B20 [MODIFIED]: 买家订单查询 / 按状态过滤
- [ ] B21 [MODIFIED]: 买家订单查询 / 列表不带状态参数
- [ ] B22 [MODIFIED]: 买家订单查询 / 订单详情内容
- [ ] B23 [MODIFIED]: 买家订单查询 / 查询他人订单

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 下单并发集成测试通过（`backend/tests/integration/order_concurrency_test.go`：不超卖、不少扣、并发幂等只一单）
- [ ] Q3: 事务原子性测试（扣减中途失败 → 余额、库存、订单、购物车、流水全部不变）
- [ ] Q4: 幂等安全测试（重放 200、同键异内容 409、失败后同键可重试）
- [ ] Q5: 越权访问他人订单/地址/购物车行一律 404 的安全用例

## Schema Changes
无新增（orders、order_items、user_addresses 现有结构满足）。

## Follow-ups
- FU-f2c8b347: 删除默认地址后允许暂无默认还是自动顶替，待产品口径。
- FU-6d2a90f3: `request_hash` 中 `coupon_id` 的规范化编码（未选券与选券两种输入的字节级编码）发布前冻结，沿用主档"编码冻结"条款。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
