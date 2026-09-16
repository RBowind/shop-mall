# Sprint Contract: admin-authz
Source: specs/admin-authz/spec.md
Status: DRAFT（待独立 Evaluator 与人 gate）

## Behavioral Changes

- [ ] B1 [ADDED]: 管理员登录 / 登录成功
- [ ] B2 [ADDED]: 管理员登录 / 登录失败
- [ ] B3 [ADDED]: 管理员登录 / 撞库限速
- [ ] B4 [ADDED]: 管理员登录 / 已停用账号登录
- [ ] B5 [ADDED]: 写请求 CSRF 与 Origin 校验 / 缺少 CSRF 头
- [ ] B6 [ADDED]: 写请求 CSRF 与 Origin 校验 / 非白名单来源
- [ ] B7 [ADDED]: 写请求 CSRF 与 Origin 校验 / 登录例外
- [ ] B8 [ADDED]: 会话即时失效 / 改密成功旧会话失效
- [ ] B9 [ADDED]: 会话即时失效 / 新密码强度不足
- [ ] B10 [ADDED]: 会话即时失效 / 登出
- [ ] B11 [ADDED]: 会话即时失效 / 停用账号
- [ ] B12 [ADDED]: 会话即时失效 / 会话到期
- [ ] B13 [ADDED]: 权限实时判定 / 撤权立即生效
- [ ] B14 [ADDED]: 权限实时判定 / 授权立即生效
- [ ] B15 [ADDED]: 权限实时判定 / 越权访问
- [ ] B16 [ADDED]: 权限实时判定 / 买家令牌打管理域
- [ ] B17 [ADDED]: 权限实时判定 / 管理令牌打买家域
- [ ] B18 [ADDED]: 权限实时判定 / 两域密钥配置相同
- [ ] B19 [ADDED]: 角色与管理员账号管理 / 创建与改授权
- [ ] B20 [ADDED]: 角色与管理员账号管理 / 创建重名角色
- [ ] B21 [ADDED]: 角色与管理员账号管理 / 删除被引用角色
- [ ] B22 [ADDED]: 角色与管理员账号管理 / 禁用最后一个超管
- [ ] B23 [ADDED]: 角色与管理员账号管理 / 只读查询角色与账号
- [ ] B24 [ADDED]: 角色与管理员账号管理 / 停用账号
- [ ] B25 [ADDED]: 角色与管理员账号管理 / 未知权限码
- [ ] B26 [ADDED]: 会员查询 / 查询买家
- [ ] B27 [ADDED]: 会员查询 / 运营查询会员
- [ ] B28 [ADDED]: 审计日志留存与查询 / 成功操作留痕
- [ ] B29 [ADDED]: 审计日志留存与查询 / 失败尝试同样留痕
- [ ] B30 [ADDED]: 审计日志留存与查询 / 查询审计
- [ ] B31 [ADDED]: 审计日志留存与查询 / 敏感信息不进日志
- [ ] B32 [MODIFIED]: 登出与改密挂 `admin:self` 权限码 / 缺 admin:self 的账号
- [ ] B33 [MODIFIED]: 登出与改密挂 `admin:self` 权限码 / 默认角色不受影响

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 鉴权矩阵全覆盖安全用例（`backend/tests/`：每权限码至少一条 403 路径）
- [ ] Q3: CSRF/Origin 中间件单测（缺头、错值、白名单内外）
- [ ] Q4: 日志敏感字段扫描通过（B30 对应，N-002）
- [ ] Q5: 前端 admin：构建+类型检查+单测绿（N-008）

## Schema Changes
无新增（admin_users、roles、permissions、role_permissions、audit_logs 现有结构满足）。

## Follow-ups
- FU-1c6f9b3e（沿用 techspec）: 审计摘要与列表脱敏格式待定。
- FU-8f1d3b64: 错误响应统一回传 trace_id 的归属 spec 待确认（属全局接口规范，techspec §5 统一响应）。
- FU-5a9c7e36: 角色/管理员写操作前端未接线（PRD §8-2，ADMIN_TARGET 既定未完成项）。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
