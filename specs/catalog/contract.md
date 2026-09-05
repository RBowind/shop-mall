# Sprint Contract: catalog
Source: specs/catalog/spec.md
Status: APPROVED（人 gate 已过，2026-09-01；2026-09-02 由 devloop 启动时经用户确认统一改标记）

## Behavioral（from spec scenarios）
- [ ] B1: 列表——未登录分页仅 on_sale，id 降序 — verified by BDD test
- [ ] B2: 详情在售——名称图集价库存描述 — verified by BDD test
- [ ] B3: 详情不可见——不存在或已下架 404 — verified by BDD test
- [ ] B4: 缺货商品仍可见——库存 0 正常返回 — verified by BDD test
- [ ] B5: 目录——静态键×在售计数，不分页 — verified by BDD test
- [ ] B6: 按分类筛选 — verified by BDD test
- [ ] B7: 子串命中——大小写不敏感 — verified by BDD test
- [ ] B8: 空结果——total=0 非错误 — verified by BDD test
- [ ] B9: 结果上限——最多 50 条 — verified by BDD test
- [ ] B10: 图集字段——完整 URL 数组首图即主图 — verified by BDD test
- [ ] B11: 管理端列表按状态筛选——可见已下架 — verified by BDD test
- [ ] B12: 新建商品——默认在售+成功审计 — verified by BDD test
- [ ] B13: 下架即时生效——买家端即不可见，快照不受影响+成功审计 — verified by BDD test
- [ ] B14: 编辑与重新上架——买家端即见新值+成功审计 — verified by BDD test
- [ ] B15: 未知分类键——422 商品不变 — verified by BDD test
- [ ] B16: 无写权限——403+失败审计 — verified by BDD test
- [ ] B17: 合法上传——返回 object key+成功审计 — verified by BDD test
- [ ] B18: 非图片文件——按内容与解码判定拒绝 — verified by BDD test
- [ ] B19: 超过大小上限——2MB 外拒绝 — verified by BDD test

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 上传伪装测试（改扩展名的可执行文件/文本被拒）
- [ ] Q3: 免登录边界测试（公开端点无需令牌；管理写端点无令牌 401/无权限 403）
- [ ] Q4: 分页翻页一致性测试（不重复不遗漏）

## Schema Changes
无新增（products 现有结构满足）。

## Follow-ups
- FU-b3e7d941: 图集超 9 张时服务端拒绝还是截断，待定。
- FU-c8a2f516: PRD F-603"图片支持上下架"无数据模型落点，按字面实现还是删条目，待裁决。
- FU-d41b9e73: F-201"推荐商品"取数口径（techspec 无推荐端点），待产品定义。
- FU-a1f4c902: openapi.yaml 缺失，`make contract-check` 门禁待其入库。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
