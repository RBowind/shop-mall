# Sprint Contract: admin-authz
Source: specs/admin-authz/spec.md
Status: APPROVED（人 gate 已过，2026-09-01；2026-09-02 由 devloop 启动时经用户确认统一改标记）

## Behavioral（from spec scenarios）
- [ ] B1: 登录成功——Cookie 三属性+CSRF 令牌+last_login+成功审计 — verified by BDD test
- [ ] B2: 登录失败——统一错误+失败审计 — verified by BDD test
- [ ] B3: 撞库限速——账号与来源 IP 双维度拒绝 — verified by BDD test
- [ ] B4: 已停用账号登录——被拒 — verified by BDD test
- [ ] B5: 缺少 CSRF 头——写请求被拒 — verified by BDD test
- [ ] B6: 非白名单来源——写请求被拒 — verified by BDD test
- [ ] B7: 登录例外——无 Cookie 阶段不做 CSRF 校验 — verified by BDD test
- [ ] B8: 改密成功旧会话失效——旧 Cookie 401 — verified by BDD test
- [ ] B9: 新密码强度不足——拒绝且旧密码有效 — verified by BDD test
- [ ] B10: 登出——旧 Cookie 401 — verified by BDD test
- [ ] B11: 停用账号（会话即时失效）——旧令牌下一次请求 401 — verified by BDD test
- [ ] B12: 会话到期——30 分钟后 401 — verified by BDD test
- [ ] B13: 撤权立即生效——降角色后审批退款 403 — verified by BDD test
- [ ] B14: 授权立即生效——授 audit:read 免重登可查 — verified by BDD test
- [ ] B15: 越权访问——403+失败审计 — verified by BDD test
- [ ] B16: 买家令牌打管理域——401 — verified by BDD test
- [ ] B17: 管理令牌打买家域——401 — verified by BDD test
- [ ] B18: 两域密钥配置相同——服务拒绝启动 — verified by BDD test
- [ ] B19: 创建与改授权——生效，审计按审计 Requirement 记录 — verified by BDD test
- [ ] B20: 删除被引用角色——拒绝 — verified by BDD test
- [ ] B21: 禁用最后一个超管——拒绝 — verified by BDD test
- [ ] B22: 只读查询角色与账号——权限码集合与启用状态 — verified by BDD test
- [ ] B23: 停用账号（账号管理）——保留可查无法登录，失效引用"会话即时失效" — verified by BDD test
- [ ] B24: 未知权限码——422 角色不变 — verified by BDD test
- [ ] B25: 查询买家——数字按 ID 否则昵称，含余额与注册时间 — verified by BDD test
- [ ] B26: 运营查询会员——403 — verified by BDD test
- [ ] B27: 成功操作留痕——result=success 含 trace_id — verified by BDD test
- [ ] B28: 失败尝试同样留痕——result=failure — verified by BDD test
- [ ] B29: 查询审计——倒序分页 — verified by BDD test
- [ ] B30: 敏感信息不进日志——令牌/密码/code/session_key 不出现，手机号地址仅脱敏 — verified by BDD test
- [ ] B31: 缺 admin:self 的自定义角色——登出/改密 403（守卫补齐，收紧现路由） — verified by BDD test
- [ ] B32: 默认角色不受影响——登出/改密放行 — verified by BDD test

## Quality
- [ ] Q1: 后端全量测试 `go test ./...` 绿
- [ ] Q2: 鉴权矩阵全覆盖安全用例（`backend/tests/`：每权限码至少一条 403 路径）
- [ ] Q3: CSRF/Origin 中间件单测（缺头、错值、白名单内外）
- [ ] Q4: 日志敏感字段扫描通过（B30 对应，N-002）
- [ ] Q5: 前端 admin：构建+类型检查+单测绿（N-008）

## Schema Changes
无新增（admin_users、roles、permissions、role_permissions、audit_logs 现有结构满足）。

## Follow-ups
- FU-7a4d2e8b（沿用 techspec）: 管理端登录失败/限速的业务错误码段归属待定。
- FU-1c6f9b3e（沿用 techspec）: 审计摘要与列表脱敏格式待定。
- FU-6b8f2d49: CSRF/Origin 拒绝的具体响应码（401 或 403）待定。
- FU-2c5a7e91: 创建重名角色的冲突响应口径待定。
- FU-8f1d3b64: 错误响应统一回传 trace_id 的归属 spec 待确认（全局接口规范）。
- FU-5a9c7e36: 角色/管理员写操作前端未接线（PRD §8-2，ADMIN_TARGET 既定未完成项）。
- FU-a1f4c902: openapi.yaml 缺失，`make contract-check` 门禁待其入库。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
