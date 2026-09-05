# 购物车

> 买家登录后的加购与行内管理。商品状态变化在购物车中如实反映，供结算排除不可购行。

## Elaborates

- techspec: `docs/tech-specs/shop-mall-tech-spec.md` §2；`docs/tech-specs/interfaces.md` 买家域端点；`docs/tech-specs/data-model.md` cart_items

## ADDED Requirements

### Requirement: 加购需登录且同商品合并数量

The system SHALL 要求登录后加购；同一买家同一商品只保留一行，重复加购累加数量。

#### Scenario: 重复加购

- **WHEN** 已登录买家两次 `POST /api/v1/cart` 提交同一商品
- **THEN** 该买家该商品在购物车中只有一行，数量为两次之和

#### Scenario: 未登录加购

- **WHEN** 无有效买家 JWT（JSON Web Token，服务端签名令牌）请求加购
- **THEN** 响应 401，购物车不变

### Requirement: 购物车行内管理

The system SHALL 支持改数量与删除，状态持久在服务端。

#### Scenario: 改数量

- **WHEN** 买家 `PATCH /api/v1/cart/{itemId}` 提交合法新数量
- **THEN** 变更生效，重新拉取列表返回新数量

#### Scenario: 删除行

- **WHEN** 买家 `DELETE /api/v1/cart/{itemId}`
- **THEN** 该行移除，不再出现在列表

#### Scenario: 操作非本人的行

- **WHEN** itemId 不属于当前买家
- **THEN** 响应 404（不暴露资源是否存在），他人购物车不变

#### Scenario: 数量非法

- **WHEN** 提交数量小于等于 0 或非整数
- **THEN** 返回参数错误，行不变

### Requirement: 不可购标记

The system SHALL 在购物车列表中标出已下架或缺货的行，且商品下架不删除购物车记录。

#### Scenario: 管理员下架后

- **WHEN** 管理员下架购物车内某商品，买家再拉取列表
- **THEN** 该行仍在列表中被标记为不可购

#### Scenario: 库存不足

- **WHEN** 买家请求 `GET /api/v1/cart`
- **AND** 某行对应商品的当前库存小于该行数量
- **THEN** 该行被标记为不可购

#### Scenario: 客户端拦截结算

- **WHEN** 买家在小程序点击"去结算"
- **AND** 购物车存在不可购行
- **THEN** 小程序只提交可购行，并给出拦截提示

## Coverage Gaps

- 直接调用加购接口提交一个已下架或零库存商品，服务端接受还是拒绝、用什么错误码，techspec 与 PRD 均未定义。
