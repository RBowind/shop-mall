# Sprint Contract: checkout
Source: specs/checkout/spec.md
Status: APPROVED（人 gate 已过，2026-09-01；2026-09-02 由 devloop 启动时经用户确认统一改标记）

## Behavioral（from spec scenarios）
- [ ] B1: 新增地址——version=1，首个不自动默认 — verified by BDD test
- [ ] B2: 设默认顶替旧默认 — verified by BDD test
- [ ] B3: 并发设默认——最终恰有一个且为二者之一 — verified by BDD test
- [ ] B4: 编辑递增版本 — verified by BDD test
- [ ] B5: 地址列表——含默认标记与版本号 — verified by BDD test
- [ ] B6: 删除地址——列表不再返回，历史快照不受影响 — verified by BDD test
- [ ] B7: 操作非本人地址——404 — verified by BDD test
- [ ] B8: 下单成功——同事务扣库存扣积分建单流水删行+成交时间 — verified by BDD test
- [ ] B9: 请求体不携带价格与身份——服务端不采信 — verified by BDD test
- [ ] B10: 积分不足——5xxx 整体回滚且键不留存可重试 — verified by BDD test
- [ ] B11: 库存不足——3xxx 整体回滚 — verified by BDD test
- [ ] B12: 提交含已下架商品的行——3xxx 回滚 — verified by BDD test
- [ ] B13: 提交非本人的行或地址——404 无副作用 — verified by BDD test
- [ ] B14: 原样重放——返回原订单无新副作用 — verified by BDD test
- [ ] B15: 同键不同内容——409 — verified by BDD test
- [ ] B16: 内容一致的判定范围——改行/数量/地址版本即新意图 — verified by BDD test
- [ ] B17: 并发双击提交——只一单，另一重放或 409 — verified by BDD test
- [ ] B18: 商品后续变更不影响——详情返回成交快照 — verified by BDD test
- [ ] B19: 按状态过滤——五状态一一对应 — verified by BDD test
- [ ] B20: 列表不带状态参数——返回本人全部 — verified by BDD test
- [ ] B21: 订单详情内容——含退款原因与拒绝原因 — verified by BDD test
- [ ] B22: 查询他人订单——404 — verified by BDD test

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
- FU-a1f4c902: openapi.yaml 缺失，下单请求体字段级校验以入库后的契约为断言依据。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
