# 管理员认证与权限

> 后台以用户名口令换取 Cookie（浏览器随请求自动携带的会话状态标识）会话，权限按角色实时判定；角色、账号、会员查询与审计日志的受控管理。

## Elaborates

- techspec: `docs/tech-specs/shop-mall-tech-spec.md` §2、§5；`docs/tech-specs/flows.md` 第 5 节；`docs/tech-specs/interfaces.md` 鉴权矩阵；`docs/tech-specs/data-model.md` 权限域与账务域 audit_logs

## ADDED Requirements

### Requirement: 管理员登录

The system SHALL 校验用户名与密码后下发 HttpOnly（脚本不可读）Secure（仅经 HTTPS——超文本传输安全协议——传输）SameSite=Strict（不随跨站请求发送）Cookie 会话，有效期 30 分钟，同时返回 CSRF（Cross-Site Request Forgery，跨站请求伪造）防护令牌。

#### Scenario: 登录成功

- **WHEN** `POST /api/admin/v1/auth/login` 提交有效用户名与密码
- **THEN** 响应设置管理会话 Cookie 并返回 CSRF 令牌
- **AND** 更新账号最近登录时间，写一条成功审计

#### Scenario: 登录失败

- **WHEN** 登录请求提交不存在的用户名或错误的密码
- **THEN** 返回同一个统一错误，不区分"账号不存在"与"密码错误"
- **AND** 写一条失败审计

#### Scenario: 撞库限速

- **WHEN** 同一账号或同一来源 IP（Internet Protocol 地址，即客户端网络地址）持续高频尝试登录
- **THEN** 请求被限速拒绝，不再校验口令

#### Scenario: 已停用账号登录

- **WHEN** 已被停用（enabled=false）的账号提交登录请求
- **THEN** 拒绝登录

### Requirement: 写请求 CSRF 与 Origin 校验

The system SHALL 要求管理域写请求携带合法 CSRF 头并来自白名单 Origin（请求来源站点）；登录接口除外。

#### Scenario: 缺少 CSRF 头

- **WHEN** 会话有效的写请求不带 `X-CSRF-Token`
- **THEN** 请求被拒绝

#### Scenario: 非白名单来源

- **WHEN** 写请求的 Origin 不在白名单
- **THEN** 请求被拒绝

#### Scenario: 登录例外

- **WHEN** 登录请求（此时尚无 Cookie）
- **THEN** 不做 CSRF 校验，依靠 HTTPS、SameSite、Origin 校验与限速保护

### Requirement: 会话即时失效

The system SHALL 在改密、登出或账号停用后，使旧令牌从下一次请求起失效。

#### Scenario: 改密成功旧会话失效

- **WHEN** 管理员改密成功（须先验证旧密码）后用旧 Cookie 再发请求
- **THEN** 响应 401，须重新登录

#### Scenario: 新密码强度不足

- **WHEN** 改密请求的新密码不满足强度要求（至少 12 位，含大小写、数字、符号）
- **THEN** 拒绝修改并返回参数错误，旧密码继续有效

#### Scenario: 登出

- **WHEN** 管理员登出后用旧 Cookie 再发请求
- **THEN** 响应 401

#### Scenario: 停用账号

- **WHEN** 超管停用某管理员账号，该账号继续用有效期内的旧令牌请求
- **THEN** 下一次请求即 401

#### Scenario: 会话到期

- **WHEN** 登录成功 30 分钟后，用同一 Cookie 请求管理域接口
- **THEN** 响应 401，须重新登录

### Requirement: 权限实时判定

The system SHALL 每次请求按账号当前角色实时读取权限码，权限不写入令牌；权限变更自下一次请求生效；未列入公开清单的接口默认拒绝。

#### Scenario: 撤权立即生效

- **WHEN** 超管把某账号从超管角色改为运营角色，该账号随即请求审批退款
- **THEN** 响应 403（按新角色判定）

#### Scenario: 授权立即生效

- **WHEN** 账号被授予 `audit:read` 后请求审计列表
- **THEN** 正常返回，无需重新登录

#### Scenario: 越权访问

- **WHEN** 管理员请求其角色权限码不含的接口
- **THEN** 响应 403，且失败审计记录该尝试

#### Scenario: 买家令牌打管理域

- **WHEN** 买家 JWT（JSON Web Token，服务端签名令牌）请求 `/api/admin/v1/*` 任一端点
- **THEN** 响应 401，管理域不接受买家令牌

#### Scenario: 管理令牌打买家域

- **WHEN** 管理员会话请求需买家身份的端点
- **THEN** 响应 401，两域令牌的签发方、受众、密钥环互不相通

#### Scenario: 两域密钥配置相同

- **WHEN** 部署时买家域与管理域配置了同一签名密钥
- **THEN** 服务拒绝启动

### Requirement: 角色与管理员账号管理

The system SHALL 让持 `role:manage` 的超管创建角色、变更授权、启停账号：不删除被引用角色、不禁用最后一个超管、账号只停不删。

#### Scenario: 创建与改授权

- **WHEN** 超管新建角色或修改角色权限集合
- **THEN** 变更生效；审计按"审计日志留存与查询"Requirement 记录；受影响账号的权限判定以"权限实时判定"Requirement 为准

#### Scenario: 删除被引用角色

- **WHEN** 请求删除仍有账号挂着的角色
- **THEN** 拒绝

#### Scenario: 禁用最后一个超管

- **WHEN** 请求停用最后一个启用中的超管账号
- **THEN** 拒绝

#### Scenario: 只读查询角色与账号

- **WHEN** 持 `role:manage` 的管理员请求 `GET /api/admin/v1/roles` 或 `GET /api/admin/v1/admin-users`
- **THEN** 返回各角色的权限码集合，以及各管理员账号的角色与启用状态

#### Scenario: 停用账号

- **WHEN** 超管停用某账号
- **THEN** 账号保留可查、无法登录；其旧令牌失效行为同"会话即时失效"Requirement

#### Scenario: 未知权限码

- **WHEN** 角色授权提交系统中不存在的权限码
- **THEN** 响应 422，角色不变

### Requirement: 会员查询

The system SHALL 向持 `user:read` 的管理员提供买家列表只读查询：关键词为纯数字时按买家标识匹配，否则按昵称匹配。

#### Scenario: 查询买家

- **WHEN** `GET /api/admin/v1/users` 带关键词
- **THEN** 返回匹配的买家列表，含积分余额与注册时间

#### Scenario: 运营查询会员

- **WHEN** 角色不含 `user:read` 的管理员（如运营）请求 `GET /api/admin/v1/users`
- **THEN** 响应 403

### Requirement: 审计日志留存与查询

The system SHALL 对登录成败、改密、停用账号、商品变更、图片上传、发货、退款审批、积分调整、权限变更写只追加审计，记录操作人、角色快照、动作、对象、结果、脱敏变更摘要、操作时间与 trace_id（链路追踪标识）；审计只可查询不可改写。

#### Scenario: 成功操作留痕

- **WHEN** 上述任一管理动作成功
- **THEN** 可查到对应 `result=success` 的审计条目，含 trace_id

#### Scenario: 失败尝试同样留痕

- **WHEN** 上述任一动作因权限不足、状态冲突或参数错误失败
- **THEN** 可查到对应 `result=failure` 的审计条目

#### Scenario: 查询审计

- **WHEN** 持 `audit:read` 的管理员 `GET /api/admin/v1/audit-logs`
- **THEN** 倒序分页返回审计条目

#### Scenario: 敏感信息不进日志

- **WHEN** 买家下单、管理员查订单或写审计日志
- **THEN** 令牌、密码、微信 code、session_key（微信会话密钥）完全不出现在普通业务日志与审计摘要中
- **AND** 手机号、收货详细地址只以脱敏形式出现

## MODIFIED Requirements

### Requirement: 登出与改密挂 `admin:self` 权限码

The system SHALL 要求登出与改密请求通过 `admin:self` 权限码判定。两个默认角色均持有该码，对默认角色行为无变化。

#### Scenario: 缺 admin:self 的账号

- **WHEN** 某自定义角色不含 `admin:self`，其账号请求登出或改密
- **THEN** 响应 403

#### Scenario: 默认角色不受影响

- **WHEN** 运营或超管账号登出、改密
- **THEN** 正常放行

## Coverage Gaps

- 管理端登录失败（401/限速 429）归属的业务错误码段未定（2xxx 现定义为微信认证与登录态过期），沿用 techspec follow-up FU-7a4d2e8b。
- CSRF 头缺失与 Origin 非白名单的拒绝响应码未定（源材料只给"401 或 403"区间），本 spec 只约束"被拒绝"。
- 创建重名角色的冲突响应口径未定（数据模型仅约束角色名唯一，源材料未给错误码）。
- 审计摘要与列表展示的脱敏格式未定，沿用 FU-1c6f9b3e。
- 错误响应统一回传 trace_id 属全局接口规范（techspec §5 统一响应），未在本 spec 单列，归属转人确认。
