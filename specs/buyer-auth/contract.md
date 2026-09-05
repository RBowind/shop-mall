# Sprint Contract: buyer-auth
Source: specs/buyer-auth/spec.md
Status: APPROVED（人 gate 已过，2026-09-01；2026-09-02 由 devloop 启动时经用户确认统一改标记）

## Behavioral（from spec scenarios）
测试落点：`backend/tests/e2e/buyer_auth_spec_test.go`（2026-09-01 全绿，映射表在该文件头注释）。
- [x] B1: 登录成功——openid 建号/定位，返回 JWT，session_key 三禁 — `TestSpecBuyerAuthB1_SessionKeyNeverLeaksToResponse` + `TestBuyerLoginSignupBonusIsExactlyOnce`
- [x] B2: 微信侧换取失败——2xxx 错误，不建用户不签发 — `TestSpecBuyerAuthB2_WeChatFailureIs2xxxBusinessCodeAndCreatesNoUser`（实测 code=2002 ✓）
- [x] B3: 令牌过期或无效——401 — `TestSpecBuyerAuthB3_ExpiredAndForgedAndAdminTokensRejectedByBuyerAPI`
- [ ] B4: 客户端失效处理——401 后清会话引导重登 — 端内行为，归小程序 TS 套件（make test），未在本次 Go 层
- [x] B5: 新用户首登——建号+赠分+signup_bonus 流水同事务 — `TestBuyerLoginSignupBonusIsExactlyOnce` + 单元 `TestLoginUsecaseExecuteOnFirstLoginCreatesUserAndSignupBonus`
- [x] B6: 老用户重复登录——不重赠 — `TestBuyerLoginSignupBonusIsExactlyOnce`
- [x] B7: 赠送配置为 0——不更余额不写流水 — `TestSpecBuyerAuthB7_ZeroSignupBonusGrantsNothing`
- [ ] B8: 登录事务提交失败——不留存不签发 — HTTP 层不可达（需数据库故障注入）；前置失败路径由单元 `TestLoginUsecasePropagatesWeChatError`、`DoesNotInsertUserWhenSessionLookupFails` 覆盖
- [x] B9: 超限请求——预算外拒绝，不打微信 — `TestSpecBuyerAuthB9_LoginRateLimitedAfterFailedAttempts`；⚠ 实现"登录成功重置 IP 计数"与 spec 字面"每分钟 20 次"有分歧，待裁决（见 spec 冲突记录）
- [x] B10: 修改昵称——保存后全局生效 — `TestBuyerProfileGetAndUpdate`
- [x] B11: 上传头像——服务端存储返回完整地址，后续读取生效 — `TestSpecBuyerAuthB11_AvatarUploadReturnsPublicURL`
- [x] B12: 读取本人资料——GET /me 返回昵称头像余额，身份取令牌 — `TestBuyerProfileGetAndUpdate`
- [x] B13: 昵称超限——参数错误原值不变 — `TestBuyerProfileGetAndUpdate`（65 字符 → 422）

## Quality
- [x] Q1: 后端全量测试 `go test ./...` 绿（2026-09-01，见本次运行记录）
- [x] Q2: 越权/伪造令牌安全测试覆盖 B3、B9 — `TestSpecBuyerAuthB3/B9`
- [x] Q3: 登录链路日志不含 code、session_key、令牌明文 — 单元 `TestLoginUsecaseDoesNotLeakSessionKeyInLogsOrResult` + `TestSpecBuyerAuthB1`（响应面）
- [x] Q4: 赠分"每用户至多一次"有并发用例 — `TestSpecBuyerAuthQ4_ConcurrentSameOpenidLoginsGrantBonusOnce`（HTTP 层）+ `TestLoginUsecaseConcurrentFirstLoginCreatesExactlyOneUser`（单元）

## Schema Changes
无新增（users、points_ledger 现有结构满足）。

## Follow-ups
- FU-a1f4c902: `docs/api/openapi.yaml` 缺失（PRD §8-4），找回入库后 `make contract-check` 方可作为门禁。

## Pass Rule
ALL B* 断言全过 + ALL Q* + lint/test 绿。
