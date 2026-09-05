# AGENTS.md — AI 工作入口

电商小程序：积分下单、发货、退款的商城。Taro 小程序 + Go 单体后端 + Ant Design Pro 管理后台。本文件只指路，不装知识，每个文档自己看。

## 怎么跑

| 模块 | 目录 | 常用命令 |
|---|---|---|
| 后端 Go | backend/ | `make test` · `make lint` · `make build` |
| 小程序 | miniapp/ | `pnpm test` · `pnpm build` |
| 管理后台 | admin/ | `pnpm test` · `pnpm build` |
| 部署 | deploy/ | Docker Compose，手册见 docs/ops/ |

## 文档地图

- `docs/prd.md` — 需求与验收标准。做什么、不做什么，以它为准。
- `docs/api/openapi.yaml` — 接口契约唯一来源。新增字段、改接口先对它。
- `docs/architecture/` — 系统设计。00 总览（拓扑、选型、端间契约），01-04 分端，05 数据库，06 起为 feature 级 techspec。
- `docs/tech-specs/` — 系统级 techspec 文档集（入口 shop-mall-tech-spec.md + 数据模型/流程/接口详情）。与 architecture 冲突时以 migration、代码和 openapi 反映的现状为准。
- `docs/decisions/` — 架构决策记录，一决策一文件。
- `docs/postmortems/` — 事故复盘：时间线、根因、改进项。
- `docs/proposals/` — 变更提案，按模块分目录。
- `docs/specs/` — 行为契约与交付清单，一能力块一目录。
- `docs/ops/` — 运维手册。

## 工作约定

- 加功能、修 bug、调需求：先写提案进 `docs/proposals/<模块>/`，人拍板后再动代码。一个提案只解决一件事，大了拆。
- `docs/specs/<capability>/spec.md` 是实现期间的行为契约，代码与它有分歧时，回 spec 对轴。
- 手机号、地址、微信 code、密钥不进日志；金额、积分、库存、数量一律整数（细则见 PRD 非功能需求）。
- 涉及积分、权限的改动：AI 只写实现，校验逻辑不得简化；提交人对改动负全责。
- 后端 spec 验收测试一律用标准 `testing` + `TestSpec<Capability><Bn>_<Desc>` 命名，落 `backend/tests/e2e/`（每 capability 一个 `<capability>_spec_test.go`，文件头放 B↔用例映射表）；团队测试规范里的 Ginkgo 模板在本 repo 被此既有基建覆盖，不再适用。
