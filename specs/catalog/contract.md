# Sprint Contract: catalog
Source: specs/catalog/spec.md
Status: APPROVED

## Behavioral Changes

- [ ] B1 [ADDED]: 免登录浏览在售商品 / 列表
- [ ] B2 [ADDED]: 免登录浏览在售商品 / 详情在售
- [ ] B3 [ADDED]: 免登录浏览在售商品 / 详情不可见
- [ ] B4 [ADDED]: 免登录浏览在售商品 / 缺货商品仍可见
- [ ] B5 [ADDED]: 分类目录与筛选 / 目录
- [ ] B6 [ADDED]: 分类目录与筛选 / 按分类筛选
- [ ] B7 [ADDED]: 商品名搜索 / 子串命中
- [ ] B8 [ADDED]: 商品名搜索 / 空结果
- [ ] B9 [ADDED]: 商品名搜索 / 结果上限
- [ ] B10 [ADDED]: 图片地址由服务端给出 / 图集字段
- [ ] B11 [ADDED]: 管理端商品维护 / 管理端列表按状态筛选
- [ ] B12 [ADDED]: 管理端商品维护 / 新建商品
- [ ] B13 [ADDED]: 管理端商品维护 / 下架即时生效
- [ ] B14 [ADDED]: 管理端商品维护 / 编辑与重新上架
- [ ] B15 [ADDED]: 管理端商品维护 / 未知分类键
- [ ] B16 [ADDED]: 管理端商品维护 / 无写权限
- [ ] B17 [ADDED]: 商品图上传 / 合法上传
- [ ] B18 [ADDED]: 商品图上传 / 非图片文件
- [ ] B19 [MODIFIED]: 商品图上传 / 上传超限图片
- [ ] B20 [MODIFIED]: 商品图上传 / 图集超过上限

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 上传伪装测试（改扩展名的可执行文件/文本被拒）
- [ ] Q3: 免登录边界测试（公开端点无需令牌；管理写端点无令牌 401/无权限 403）
- [ ] Q4: 分页翻页一致性测试（不重复不遗漏）

## Schema Changes
无新增（products 现有结构满足）。

## Follow-ups
- FU-c8a2f516: PRD F-603"图片支持上下架"无数据模型落点，按字面实现还是删条目，待裁决。
- FU-d41b9e73: F-201"推荐商品"取数口径（techspec 无推荐端点），待产品定义。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
