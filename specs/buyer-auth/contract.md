# Sprint Contract: buyer-auth
Source: specs/buyer-auth/spec.md
Status: DRAFT（待独立 Evaluator 与人 gate）

## Behavioral Changes

- [ ] B1 [ADDED]: 微信登录签发买家令牌 / 登录成功 — verified by BDD test
- [ ] B2 [ADDED]: 微信登录签发买家令牌 / 微信侧换取失败 — verified by BDD test
- [ ] B3 [ADDED]: 微信登录签发买家令牌 / 令牌过期或无效 — verified by BDD test
- [ ] B4 [ADDED]: 微信登录签发买家令牌 / 客户端失效处理 — verified by BDD test
- [ ] B5 [ADDED]: 首次登录赠送积分 / 新用户首登 — verified by BDD test
- [ ] B6 [ADDED]: 首次登录赠送积分 / 老用户重复登录 — verified by BDD test
- [ ] B7 [ADDED]: 首次登录赠送积分 / 赠送配置为 0 — verified by BDD test
- [ ] B8 [ADDED]: 首次登录赠送积分 / 登录持久化失败 — verified by BDD test
- [ ] B9 [ADDED]: 登录接口按 IP 限流 / 超限请求 — verified by BDD test
- [ ] B10 [ADDED]: 买家资料维护 / 修改昵称 — verified by BDD test
- [ ] B11 [ADDED]: 买家资料维护 / 上传头像 — verified by BDD test
- [ ] B12 [ADDED]: 买家资料维护 / 读取本人资料 — verified by BDD test
- [ ] B13 [ADDED]: 买家资料维护 / 昵称超限 — verified by BDD test

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
