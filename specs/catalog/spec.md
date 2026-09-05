# 商品域（浏览与管理）

> 买家免登录浏览在售商品（列表、分类、搜索、详情）；管理员维护商品与商品图，上下架即时生效。

## Elaborates

- techspec: `docs/tech-specs/shop-mall-tech-spec.md` §2、§5；`docs/tech-specs/interfaces.md` 买家域端点；`docs/tech-specs/data-model.md` 商品域

## ADDED Requirements

### Requirement: 免登录浏览在售商品

The system SHALL 公开商品列表与详情，未登录可访问、可分页，只返回在售（`status=on_sale`）商品。

#### Scenario: 列表

- **WHEN** 未登录请求 `GET /api/v1/products`
- **THEN** 返回在售商品的分页列表，按 id 降序
- **AND** 分页参数 page 从 1 起、page_size 默认 20 上限 100，逐页翻取不重复不遗漏

#### Scenario: 详情在售

- **WHEN** 未登录请求 `GET /api/v1/products/{productId}` 且商品在售
- **THEN** 返回名称、有序图集、积分价、库存、图文描述

#### Scenario: 详情不可见

- **WHEN** 请求详情的商品不存在或已下架
- **THEN** 返回 404

#### Scenario: 缺货商品仍可见

- **WHEN** 未登录请求 `GET /api/v1/products` 或 `GET /api/v1/products/{productId}`
- **AND** 该商品在售且库存为 0
- **THEN** 正常返回库存为 0 的数据，购买入口由客户端据此禁用

### Requirement: 分类目录与筛选

The system SHALL 提供固定分类目录及各分类的在售实时计数，并支持列表按分类筛选。

#### Scenario: 目录

- **WHEN** 请求 `GET /api/v1/categories`
- **THEN** 返回静态目录键（`digital`、`home`、`beauty`、`food`、`apparel`）各配的中文名、展示顺序与在售商品实时计数，不分页

#### Scenario: 按分类筛选

- **WHEN** `GET /api/v1/products` 带 category 参数
- **THEN** 只返回该分类的在售商品；不带或为空时返回全部在售

### Requirement: 商品名搜索

The system SHALL 支持按商品名子串、大小写不敏感搜索。

#### Scenario: 子串命中

- **WHEN** `GET /api/v1/products` 带 keyword=手机
- **THEN** 名称包含"手机"的商品（如"手机壳"）出现在结果中

#### Scenario: 空结果

- **WHEN** 未登录发起 keyword 无匹配的 `GET /api/v1/products` 请求
- **THEN** 返回空列表且 total=0，不是错误

#### Scenario: 结果上限

- **WHEN** 搜索请求命中超过 50 条商品
- **THEN** 最多返回 50 条

### Requirement: 图片地址由服务端给出

The system SHALL 返回完整可访问的图片 URL（统一资源定位符），顺序即图集顺序、首图为主图；客户端不自行拼接域名。

#### Scenario: 图集字段

- **WHEN** 请求 `GET /api/v1/products` 或 `GET /api/v1/products/{productId}`
- **THEN** 图片字段为完整 URL 数组，第一个元素即主图

### Requirement: 管理端商品维护

The system SHALL 让持 `product:read` / `product:write` 的管理员查询、新建、编辑商品与上下架，变更即时对买家端生效。

#### Scenario: 管理端列表按状态筛选

- **WHEN** 管理员 `GET /api/admin/v1/products` 带状态筛选
- **THEN** 返回对应状态的商品；管理端视图可见已下架商品（不受"仅在售"限制）

#### Scenario: 新建商品

- **WHEN** 管理员 `POST /api/admin/v1/products` 提交名称、描述、分类、积分价、库存
- **THEN** 创建成功且默认为在售；必填与取值校验由服务端强制执行
- **AND** 一条成功审计记录本次变更

#### Scenario: 下架即时生效

- **WHEN** 管理员 `PATCH /api/admin/v1/products/{productId}` 把商品状态改为 off_sale
- **THEN** 买家端列表与详情随即不再返回该商品；历史订单的商品快照不受影响（快照契约见 `specs/checkout/spec.md`）
- **AND** 一条成功审计记录本次变更

#### Scenario: 编辑与重新上架

- **WHEN** 管理员 `PATCH /api/admin/v1/products/{productId}` 修改积分价、名称、图集，或把状态改回 on_sale
- **THEN** 买家端列表与详情随即返回新值
- **AND** 一条成功审计记录本次变更

#### Scenario: 未知分类键

- **WHEN** 新建或编辑商品提交目录之外的分类值
- **THEN** 响应 422，商品不变

#### Scenario: 无写权限

- **WHEN** 管理员发起 `POST /api/admin/v1/products` 或 `PATCH /api/admin/v1/products/{productId}`
- **AND** 其角色不含 `product:write`
- **THEN** 响应 403，且失败审计记录该尝试

### Requirement: 商品图上传

The system SHALL 只接受 JPEG、PNG、WebP 图片且单文件不超过 2MB，上传成功的图片以服务端 object key（文件标识）返回供商品引用。

#### Scenario: 合法上传

- **WHEN** 持 `image:write` 的管理员请求 `POST /api/admin/v1/images` 上传合法图片
- **THEN** 返回服务端生成的 object key，商品编辑时按数组顺序引用
- **AND** 一条成功审计记录本次上传

#### Scenario: 非图片文件

- **WHEN** 上传的文件实际格式不是 JPEG/PNG/WebP（按文件内容与解码判定，不看扩展名）
- **THEN** 拒绝，不存储

#### Scenario: 超过大小上限

- **WHEN** 上传的文件超过 2MB
- **THEN** 服务端拒绝存储，不保存该文件（上传前压缩属小程序端行为，归端内架构文档）

## Coverage Gaps

- 商品图集最多 9 张、第一张为主图是产品规则；超出 9 张时服务端拒绝还是截断未定义。
- PRD F-201"推荐商品"双列网格无服务端契约出处（techspec 无推荐端点），取数口径转人。
- PRD F-603"图片支持上下架"在数据模型无落点（商品只有整品级状态），按字面做独立图片状态还是删该条目，转人裁决。
- 商品目录键的增删属产品级变更，走变更提案，不在本 spec 范围。
