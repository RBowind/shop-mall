# Sprint Contract: cart
Source: specs/cart/spec.md
Status: APPROVED（人 gate 已过，2026-09-01；2026-09-02 由 devloop 启动时经用户确认统一改标记）

## Behavioral（from spec scenarios）
- [x] B1: 重复加购——同商品一行数量累加 — `TestSpecCartB1_DuplicateAddMergesIntoOneRowWithAccumulatedQuantity`（tests/e2e/cart_spec_test.go）。2026-09-02 devloop：首遍即绿（行为先于 contract 已实现，绿口径经用户确认），test-reviewer PASS；单例 -v 真命中 PASS 0.49s，全量 e2e 包 `go test ./tests/e2e/ -count=1` ok 8.25s
- [x] B2: 未登录加购——401 购物车不变 — `TestSpecCartB2_AddWithoutValidBuyerJWTRejected401CartUnchanged`。2026-09-02 devloop：首遍即绿（实现先于 contract），reviewer PASS：正对照 200 防一律拒假绿，6 种无效凭据形态逐一 401 + 每变体后 DB 直查不变；全量 e2e ok 7.6s，gofmt/vet 干净
- [x] B3: 改数量——生效且持久 — `TestSpecCartB3_UpdateQuantityTakesEffectAndPersists`。2026-09-02 devloop：首遍即绿（实现先于 contract），reviewer PASS：增大/减小/连续改/改回原值逐条断具体值，每 PATCH 后响应体+按 itemId 直查 DB+重新 GET 列表三点验证，多行隔离 B 行不动；全量 e2e ok 7.9s，gofmt/vet 干净
- [x] B4: 删除行——不再出现在列表 — `TestSpecCartB4_DeleteRowNoLongerAppearsInList`。2026-09-02 devloop：首遍即绿（实现先于 contract），reviewer PASS：列表侧目标商品缺席+他行 quantity 不动，DB 直查 0 行+被删主键全表查无+同 openid 重登列表仍无（含 user id 一致守卫），删后再加购=全新行（id 不同、quantity 不累加）；全量 e2e ok 8.7s，gofmt/vet 干净，无 Warning
- [x] B5: 操作非本人的行——404 他人不变 — `TestSpecCartB5_OperateForeignCartItemRejected404ForeignCartUnchanged`（兼 contract Q2 越权专用例）。2026-09-02 devloop：首遍即绿，reviewer PASS 并实跑复核：正对照防一律 404 假绿；PATCH/DELETE × 他人两行+不存在行 6 组合全 404/code=1004；他人真实行与不存在行的 code+message 同构（不暴露存在性，未钉文案）；每次拒绝后按主键 DB 直查不变+双方列表复核；全量 e2e ok 7.9s。遗留 1 条 Warning（同构比较未含 data 字段）转下轮加固
- [x] B6: 数量非法——参数错误行不变 — `TestSpecCartB6_InvalidQuantityRejectedWith4xxAndCartRowUnchanged`。2026-09-02 devloop：首遍即绿，reviewer PASS：6 非法形态（0/-1/"abc"/1.5/null/缺字段）× POST 双目标 × PATCH = 18 条逐条 400/code=1001 + 每次拒绝后 DB 直查不变，正对照防一律拒假绿，收尾全表 quantity≤0 扫描 0 行；超大值族与累加越界 422/2002 按"只观测不 invent"处理；B5 data 同构加固复跑绿；全量 e2e ok 8.3s。遗留 Warning：quantity 上限口径（int32max 接受、超 int32 400、累加 422）spec 未定义，转人裁决是否补 Scenario/FU
- [x] B7: 管理员下架后——行保留且标记不可购 — `TestSpecCartB7_AdminOffSaleKeepsCartRowMarkedNonPurchasable`。2026-09-02 devloop：首遍即绿，reviewer PASS：下架走真实管理端路由 PATCH /api/admin/v1/products/{id}（SuperAdmin+product:write，真实 Bootstrap+login），列表断目标行 id/quantity 不变且 product.status=off_sale、在售对照不误标，DB 三面直查（products 未删、cart_items 保留、主键复核），连拉稳定+他行 PATCH 隔离；同值 PATCH 与重新上架只观测未 invent；全量 e2e ok。reviewer Warning（purchasable 字段存在性）已由主 agent 代码级核实关闭（cart_handler.go:204-218 无该字段）；遗留转人：下架行改数量当前返回 200 的口径 spec 未定义
- [x] B8: 库存不足——行标记不可购 — `TestSpecCartB8_InsufficientStockMarksCartRowNonPurchasable`。2026-09-02 devloop：首遍即绿，reviewer PASS：库存变更全程走管理端真实 PATCH（无 DB 后门）；边界矩阵 S=Q 不标/S=Q-1 标/S=0 标/S>Q 对照不误标/回补列表立即恢复可购（钉死实时性）；每次 PATCH 后直查 products.stock+cart_items 主键；GET 不消耗库存用双表 reflect.DeepEqual 全库快照比对（先断基数防空比空）；assertRowPurchasability 方向自校验守卫防期望写反；全量 e2e ok 8.8s。reviewer 裁定：按实际可观测面（行内 stock<quantity，status 恒 on_sale）断言构成对 spec 的忠实覆盖；"标记无独立字段载体"属 spec 措辞与实现的解释性差异，转 sddspec 裁决
- [ ] B9: 客户端拦截结算——只提交可购行并提示（端内，E2E 覆盖） — 端内行为，归小程序 TS 套件（pnpm test），不进 Go 循环。2026-09-02 人裁决记录：核心逻辑已有部分覆盖（miniapp/test/components.test.mjs:67 等 3 例：剔除不可购行+可购性判定+禁提交），页面 toast 分支（cart/index.tsx:50-54、checkout/index.tsx:123-136）留白待前端流程补；**判定口径裂缝**：小程序 `off_sale || stock<=0` vs server spec `stock<行数量`，stock>0 但不足的行双端不一致，并入下方 FU 指针走 sddspec 裁决

## Quality
- [x] Q1: 后端全量测试 `go test ./...` 绿 — 2026-09-02 devloop 收尾兜底：26 包全 ok；tests/e2e ok 11.6s、tests/security ok 8.1s
- [x] Q2: 越权行操作安全测试（B5 有专用例） — `TestSpecCartB5_OperateForeignCartItemRejected404ForeignCartUnchanged`（PATCH/DELETE×他人行/不存在行 6 组合 404 同构，DB 复核他人不变）
- [x] Q3: 加购并发合并测试（同商品并发加购数量正确、无重复行） — `TestCartServiceConcurrentAddsAccumulateWithoutLoss`（internal/cart/service_test.go，Q* 自查判存量断言不足后补强：12 并发 Add 返回 ID 全等 + DB 断 rowCount==1 且 sum(quantity)==24；-race 下 PASS 52.9s、全包 ok，reviewer 增量评审 PASS）

## Schema Changes
无新增（cart_items 现有结构满足）。

## Follow-ups
- FU-e59a3c18: 直接加购已下架/零库存商品的服务端口径（接受还是拒绝、错误码）待定。
- FU-a1f4c902: openapi.yaml 缺失，契约门禁待其入库。
- FU-cart-spec-delta（2026-09-02 人裁决：合并一次 sddspec delta 处理，不阻塞本轮提测；提测说明须引用）：
  1. "标记为不可购"在接口/数据模型无字段载体（现实现=行内 quantity+实时 stock 被动比对，status 仅下架面）——裁决改 spec 措辞钉判定口径，还是加显式 purchasable 字段回填测试；含 B9 记录的双端口径裂缝（小程序 stock<=0 vs spec stock<quantity）。
  2. quantity 上限口径（实测：int32max 内接受、超 int32 拒 400/1001、累加溢出 422/2002）spec 未定义，是否补 Scenario。
  3. 下架行改数量实测接受（200），spec 未定义允许与否。
  4. =FU-e59a3c18（同波处理）。
  5. 低优先（人裁决挂账）：service_test.go 并发用例 goroutine 内 `ids[idx]=item.ID` 未先判 err，失败时红因被 panic 污染，下次触及该文件顺手修。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
