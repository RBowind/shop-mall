# Sprint Contract: order-payment
Source: specs/order-payment/spec.md
Status: APPROVED

## Behavioral Changes

- [ ] B1 [ADDED]: 确认支付 / 支付成功
- [ ] B2 [ADDED]: 确认支付 / 余额不足
- [ ] B3 [ADDED]: 确认支付 / 非待支付状态支付
- [ ] B4 [ADDED]: 确认支付 / 并发与重复支付只成交一次
- [ ] B5 [ADDED]: 确认支付 / 支付他人订单
- [ ] B6 [ADDED]: 支付时限 / 时限随订单生成
- [ ] B7 [ADDED]: 支付时限 / 取消处理前仍可支付
- [ ] B8 [ADDED]: 超时自动取消 / 超时订单被取消
- [ ] B9 [ADDED]: 超时自动取消 / 取消与支付并发只有一方生效
- [ ] B10 [ADDED]: 超时自动取消 / 同一订单重复处理只释放一次
- [ ] B11 [ADDED]: 超时自动取消 / 处理失败不漏单
- [ ] B12 [ADDED]: 超时自动取消 / 取消是终态
- [ ] B13 [ADDED]: 过期券清理任务 / 过期券被置终态

## Quality
- [ ] Q1: 后端全量测试与支付、超时取消测试绿
- [ ] Q2: 支付与超时取消竞态最多一个成功，失败方无副作用
- [ ] Q3: 重复支付与重复取消不重复扣积分、回补库存或处置优惠券
- [ ] Q4: 余额不足、过期、非法状态和越权访问均有失败场景

## Schema Changes

### orders
- `status` 枚举扩为 `pending_payment`、`paid`、`shipped`、`completed`、`refund_requested`、`refunded`、`cancelled`。
- 新增 `pay_expire_at`、`discount_points`、`pay_points`、`cancelled_at`；`paid_at` 改为可空。

### products
- 新增 `hold_stock`；`stock` 表示可售库存，不含待支付订单预占量。

## Follow-ups
- FU-2a91c7d5: 超时取消与过期券处理的触发频率未定义，但必须支持到期后有限时间内处理及失败后继续处理。
- FU-5b73e0a9: 支付竞态失败与已支付状态是否使用不同业务错误码待定，当前均返回 409。
- FU-4f9c21ab: 生命周期任务每轮取批的批量大小（超时待支付订单与过期可用券各取多少行）定默认值时与 `LIFECYCLE_SCAN_INTERVAL_SECONDS` 一并冻结。
- FU-2e91a76c: 支付时限默认值 15 分钟的覆盖范围明确到业务服务水平协议（SLA）层，若未来需对外公示需补产品口径文件。

## Pass Rule
ALL Behavioral Changes + ALL Quality + lint/test 绿。
