# AGENTS.md — AI 工作入口

电商小程序：积分下单、发货、退款的商城。Taro 小程序 + Go 单体后端 + Umi Max 管理后台。本文件指路兼记运行口径，业务知识在各文档里。

## 仓库地图

| 目录 | 内容 | 工具链 | 常用命令 |
|---|---|---|---|
| `backend/` | Go 单体后端：Gin + GORM + PostgreSQL 16 + goose 迁移 | go modules | `make -C backend test`·`lint`·`build` |
| `admin/` | 管理后台 SPA：Umi Max 4.7 + React 18 + antd 5 | pnpm workspace | `pnpm --filter admin test`·`build` |
| `miniapp/` | 买家小程序：Taro 4.2 + React 18 + TDesign | pnpm workspace | `pnpm --filter miniapp test`·`build` |
| `tools/` | 门禁脚本：openapi / spec / coverage / deploy（Node）、mutation（Python） | Node / Python | 经 Makefile 调用 |
| `deploy/` | Docker Compose、备份与发布脚本 | — | `make compose-up`·`preflight`·`smoke` |

pnpm workspace 成员是 `miniapp`、`admin`、`tools/openapi`；`backend/` 走 go modules，独立于 workspace。

工具链基线：Go 1.27.1、Node 24.x、pnpm 10.33.0、Docker Compose v5.2.0。

## 怎么跑

根 Makefile 是统一入口：

```bash
make test            # backend go test + 所有前端包
make lint            # 只查 Go 的 gofmt——三个前端包都没有 lint 脚本
make build
make generate        # openapi.yaml → 两端 src/services/generated/api.d.ts
make contract-check  # redocly lint + 生成结果与签入产物比对
make diff-coverage   # 改动行覆盖率，默认 COMPARE=origin/main FAIL_UNDER=80
```

需要真实数据库的验收门在 Makefile 底部：`make test-e2e`、`test-security`、`test-concurrency`、`admin-e2e`、`restore-drill`。

## PR 门禁

`main` 是保护分支，改动走 PR。CI 按改动路径过滤，未触及的门打印 fast pass 直接绿；`unknown` 兜底负责"改动落在所有清单之外"（新顶层目录、workflow 自身等），命中时四门全跑。

| Job | 触发路径 | 内容 |
|---|---|---|
| contract | `docs/api/**`、`tools/openapi/**`、两端 `generated/**`、workspace 安装面 | `make contract-check` |
| backend | `backend/**` | gofmt、govulncheck、`go test -race ./...` |
| frontend | `admin/**`、`miniapp/**` | 两端 test 与 build |
| coverage | `backend/**`、`tools/coverage/**`、Makefile | diff-cover，改动行 ≥ 80% |
| unknown | 上述之外的一切 | 四门全跑 |

paths-filter 用 `predicate-quantifier: some-with-excludes`，改 workflow 时保持这个值——默认的 `some` 会把负向模式反转成恒真的正向匹配器，让 unknown 兜底永真、短路分支永不触发。

改接口字段先动 `docs/api/openapi.yaml`，跑 `make generate` 重新生成两端 `api.d.ts`，`make contract-check` 核对生成结果与签入产物一致。

## 文档地图

- `docs/prd.md` — 需求与验收标准。做什么、不做什么，以它为准。
- `docs/api/openapi.yaml` — 接口契约唯一来源。新增字段、改接口先对它。
- `docs/architecture/` — 系统设计。00 总览（拓扑、选型、端间契约），01-04 分端，05 数据库，06 起为 feature 级 techspec。
- `docs/tech-specs/` — 系统级 techspec 文档集（入口 shop-mall-tech-spec.md + 数据模型/流程/接口详情）。与 architecture 冲突时以 migration、代码和 openapi 反映的现状为准。
- `docs/decisions/` — 架构决策记录，一决策一文件。
- `docs/accidents/` — 踩坑与复盘：时间线、根因、改进项；不限于生产事故。
- `docs/proposals/` — 变更提案，按模块分目录。
- `specs/` — 行为契约与交付清单，一能力块一目录。
- `docs/ops/` — 运维手册。

## 工作约定

- 加功能、修 bug、调需求：先写提案进 `docs/proposals/<模块>/`，人拍板后再动代码。一个提案只解决一件事，大了拆。
- `specs/<capability>/spec.md` 是实现期间的行为契约，代码与它有分歧时，回 spec 对轴。
- 手机号、地址、微信 code、密钥不进日志；金额、积分、库存、数量一律整数（细则见 PRD 非功能需求）。
- 涉及积分、权限的改动：AI 只写实现，校验逻辑不得简化；提交人对改动负全责。
- 后端 spec 验收测试一律用标准 `testing` + `TestSpec<Capability><Bn>_<Desc>` 命名，落 `backend/tests/e2e/`（每 capability 一个 `<capability>_spec_test.go`，文件头放 B↔用例映射表）；团队测试规范里的 Ginkgo 模板在本 repo 被此既有基建覆盖，不再适用。