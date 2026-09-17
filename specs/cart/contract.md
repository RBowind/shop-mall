# Sprint Contract: cart
Source: specs/cart/spec.md
Status: APPROVED

## Behavioral Changes

- [x] B1 [ADDED]: 加购需登录且同商品合并数量 / 重复加购
- [x] B2 [ADDED]: 加购需登录且同商品合并数量 / 未登录加购
- [x] B3 [ADDED]: 购物车行内管理 / 改数量
- [x] B4 [ADDED]: 购物车行内管理 / 删除行
- [x] B5 [ADDED]: 购物车行内管理 / 操作非本人的行
- [x] B6 [ADDED]: 购物车行内管理 / 数量非法
- [x] B7 [ADDED]: 不可购标记 / 管理员下架后
- [x] B8 [ADDED]: 不可购标记 / 库存不足
- [x] B9 [ADDED]: 不可购标记 / 客户端拦截结算

## Quality
- [x] Q1: 后端全量测试 `go test ./...` 绿 — 2026-09-02 devloop 收尾兜底：26 包全 ok；tests/e2e ok 11.6s、tests/security ok 8.1s
- [x] Q2: 越权行操作安全测试（B5 有专用例） — `TestSpecCartB5_OperateForeignCartItemRejected404ForeignCartUnchanged`（PATCH/DELETE×他人行/不存在行 6 组合 404 同构，DB 复核他人不变）
- [x] Q3: 加购并发合并测试（同商品并发加购数量正确、无重复行） — `TestCartServiceConcurrentAddsAccumulateWithoutLoss`（internal/cart/service_test.go，Q* 自查判存量断言不足后补强：12 并发 Add 返回 ID 全等 + DB 断 rowCount==1 且 sum(quantity)==24；-race 下 PASS 52.9s、全包 ok，reviewer 增量评审 PASS）

## Schema Changes
无新增（cart_items 现有结构满足）。

## Follow-ups
- FU-e59a3c18: 直接加购已下架/零库存商品的服务端口径（接受还是拒绝、错误码）待定。
- FU-cart-spec-delta（2026-09-02 人裁决：合并一次 sddspec delta 处理，不阻塞本轮提测；提测说明须引用）：
  1. "标记为不可购"在接口/数据模型无字段载体（现实现=行内 quantity+实时 stock 被动比对，status 仅下架面）——裁决改 spec 措辞钉判定口径，还是加显式 purchasable 字段回填测试；含 B9 记录的双端口径裂缝（小程序 stock<=0 vs spec stock<quantity）。
  2. quantity 上限口径（实测：int32max 内接受、超 int32 拒 400/1001、累加溢出 422/2002）spec 未定义，是否补 Scenario。
  3. 下架行改数量实测接受（200），spec 未定义允许与否。
  4. =FU-e59a3c18（同波处理）。
  5. 低优先（人裁决挂账）：service_test.go 并发用例 goroutine 内 `ids[idx]=item.ID` 未先判 err，失败时红因被 panic 污染，下次触及该文件顺手修。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
