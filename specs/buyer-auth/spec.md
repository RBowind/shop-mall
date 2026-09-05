# 买家登录与账号

> 微信小程序买家以微信身份换取系统登录态，首次登录赠送积分，并维护个人资料。买家域所有写操作的前置。

## Elaborates

- techspec: `docs/tech-specs/shop-mall-tech-spec.md` §2、§5；`docs/tech-specs/flows.md` 第 1 节；`docs/tech-specs/interfaces.md` 买家域端点

## ADDED Requirements

### Requirement: 微信登录签发买家令牌

The system SHALL 用一次性登录凭证 code 换取微信用户标识 openid（openid：微信用户在本小程序内的唯一标识），返回 24 小时有效的买家 JWT（JSON Web Token，服务端签名令牌）。

#### Scenario: 登录成功

- **WHEN** 客户端以 `wx.login()` 取得一次性 code，请求 `POST /api/v1/auth/wx-login`
- **AND** 微信 code2Session（微信侧用 code 换 openid 的接口）返回有效 openid
- **THEN** 系统按 openid 建立或定位买家账号，同一 openid 恒对应同一账号
- **AND** 响应返回买家 JWT
- **AND** 微信侧会话密钥（session_key）不下发、不落库、不写普通业务日志

#### Scenario: 微信侧换取失败

- **WHEN** code 请求登录接口
- **AND** code2Session 失败（code 已被消费、过期或微信服务异常）
- **THEN** 响应返回 2xxx 段认证错误码
- **AND** 不创建用户、不签发令牌
- **AND** 客户端重新 `wx.login()` 取新 code 再试，不重复消费旧 code

#### Scenario: 令牌过期或无效

- **WHEN** 携带过期、签名不符或主体账号不存在的买家 JWT 请求买家域接口
- **THEN** 响应 401

#### Scenario: 客户端失效处理

- **WHEN** 小程序任一请求收到 401
- **THEN** 清除本地会话，下一次写操作时弹窗引导重新登录

### Requirement: 首次登录赠送积分

The system SHALL 对新买家在首次登录时一次性赠送初始积分，赠送金额来自部署配置（默认 100），每个账号终身至多一次。

#### Scenario: 新用户首登

- **WHEN** 登录请求的 openid 在系统中不存在，且部署配置的赠送积分大于 0
- **THEN** 账号创建、余额增加、一条 `type=signup_bonus` 的积分流水在同一数据库事务内完成
- **AND** 流水记录赠送后的余额

#### Scenario: 老用户重复登录

- **WHEN** 登录请求的 openid 已有账号
- **THEN** 不赠送、不写流水、余额不变

#### Scenario: 赠送配置为 0

- **WHEN** 新用户首登且部署配置的赠送积分等于 0
- **THEN** 账号创建成功，余额保持 0，不写流水

#### Scenario: 登录事务提交失败

- **WHEN** 新用户登录过程中数据库事务提交失败
- **THEN** 不签发令牌，账号与流水都不留存

### Requirement: 登录接口按 IP 限流

The system SHALL 对登录接口按来源 IP（Internet Protocol 地址，即客户端网络地址）限流，每分钟 20 次，超出拒绝。

#### Scenario: 超限请求

- **WHEN** 同一来源 IP 一分钟内第 21 次请求 `POST /api/v1/auth/wx-login`
- **THEN** 请求被拒绝，系统不发起微信侧调用

### Requirement: 买家资料维护

The system SHALL 允许已登录买家修改昵称与头像，修改后全局生效。

#### Scenario: 修改昵称

- **WHEN** 买家以有效 JWT 请求 `PATCH /api/v1/me` 提交长度与格式合法的昵称
- **THEN** 保存成功，后续所有读取返回新昵称

#### Scenario: 上传头像

- **WHEN** 买家请求 `POST /api/v1/me/avatar` 上传图片
- **THEN** 图片存于服务端，接口返回可访问的完整地址
- **AND** 后续所有读取返回新头像地址

#### Scenario: 读取本人资料

- **WHEN** 已登录买家请求 `GET /api/v1/me`
- **THEN** 返回本人昵称、头像与当前积分余额，身份只取令牌不取参数

#### Scenario: 昵称超限

- **WHEN** 提交的昵称超出长度或格式限制
- **THEN** 请求被拒，返回参数错误，原昵称不变

## Coverage Gaps

- 昵称的具体长度与字符集上限以 openapi 契约为准（当前 `docs/api/openapi.yaml` 缺失，见 follow-ups），本 spec 只约束"服务端强制限制"这一行为。
- 开发环境游客 AppID 模拟登录属开发工具便利，不列为行为契约（端内与部署口径见 `docs/architecture/`）。
