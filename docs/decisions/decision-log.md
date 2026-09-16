# 决策记录

记录本项目的关键选型和理由。格式：决策 + 选了什么 + 为什么 + 代价或边界。

## 后端栈：Go + Gin + GORM

- 为什么：编译成单个二进制，镜像小，部署简单；Gin 生态成熟，GORM 够用。
- 代价：Go 写业务比脚本语言啰嗦，但换来部署和性能的确定性。

## 结构：模块化单体，不用微服务

- 为什么：课程体量下微服务只有成本没有收益。模块边界切清楚，以后要拆才拆得动。
- 边界：跨模块副作用集中在 `internal/application` usecase 里编排，一个提交点。

## 数据库：PostgreSQL，唯一数据库

- 为什么：各环境用同一个 Docker 镜像，本地、测试、生产差异最小；约束和唯一索引做最后一道一致性防线。
- 边界：生产只做 forward migration，回滚应用不自动 goose down。

## 小程序：Taro + React

- 为什么：和管理后台复用 React 写法，一套心智两处用。
- 代价：Taro 的构建链路和原生小程序有差异，遇到坑要单独处理。

## 管理后台：React + Ant Design Pro

- 为什么：列表、表单、权限组件成熟，省时间。

## 支付：积分代替真实支付

- 为什么：避开支付资质和渠道接入，接口按真实支付的结构留，后面要接随时换。
- 边界：不接渠道，不做退款到银行卡，退的是积分。

## 图片：本地 volume + storage adapter

- 为什么：当前简单可控，通过 adapter 预留替换点。
- 边界：替换对象存储时换 adapter 实现，不碰业务代码。

## 契约：OpenAPI 作为唯一契约源

- 为什么：前端类型从 OpenAPI 生成，端间字段、错误码、鉴权只有一处真相。
- 边界：生成代码禁止手改，契约变更必须重新生成并过差异检查。

## 部署：单台 2C4G + Docker Compose

- 为什么：体量小，一台机器够；Compose 编排 nginx + 后端 + PostgreSQL。
- 边界：单点，生产要加健康检查、备份和降级预案。

## CI 门禁：合门前检查 + 合门后产物两段式

- 决策：`.github/workflows` 拆成 `pull-request-checks.yml`（合门前五道确定性检查：contract / backend / frontend / deployment / coverage）和 `release-artifacts.yml`（push main 跑主干体检并构建后端镜像）；deployment 类检查同样挂 PR 触发，迁移挂了镜像不产出。
- 为什么：单文件 ci.yml 里"挡合并的"和"出产物的"混在一起；拆开后触发器即语义。第五道 coverage 门防 AI 刷测试指标——口径只看 PR 改动行（阈值在 workflow 配置里，正文不复述数字）。变异测试每变异点重连库重跑是分钟级，当 PR 门会把等待时间炸掉，故走夜跑报告，数据够了再议升格。
- 选型记录：改动行覆盖率用 diff-cover（Bachmann1234/diff-cover）+ gocover-cobertura 转换管道，不用 go-test-coverage（vladopajic）——源码核实其 diff 是"总覆盖率相对 base 可降幅度"，不是改动行口径；axw/gocov 钉死的老版 x/tools 在 Go 1.27 编译失败，一并排除；不接 Codecov SaaS，红灯判定留在仓内可查。
- 边界：main 开 required checks + enforce admins，不开 required approvals（单人仓库，配了 PR 永远锁死）；一键发布/回滚和 claude-code-action AI 初审归下一课，本仓库不装。

## 术语：占用词根统一为 hold，lock 保留给行级并发控制

- 决策：业务占用一律用 hold——库存侧字段为 `hold_stock`（不用 `locked_stock`），券侧的占用态枚举为 `held`（不用 `locked`）。`lock` 一词专指数据库行级并发控制，不用于业务层。
- 为什么：券的占用态与库存的占用数是同一类概念，必须共用一个词。`docs/CONTEXT.md` 1.1 已把 `lock` 定义为「是机制而非业务动作；不暴露给业务层 API」；若把券状态命名为 `locked`、库存列命名为 `locked_stock`，业务层的"占用"就被命名成了数据库机制，读文档的人会以为业务层需要管行级锁。两侧当时均零实现（后端无该列，券域未开工），改动零迁移成本。
- 选型记录：另一条路是反向统一到 lock（把 `hold_stock` 改成 `locked_stock`，只动 6 处，比正向前少一半行数），弃用理由是行数省下的成本不在点上——它要推翻 1.1 那条刻意的建模边界，且会让「锁」在业务层重新变得歧义。
- 边界：中文「锁定库存」作为 `hold_stock` 的近义口语仍可在行文出现；裸「锁定」不再表示占用，只指 1.1 的行级并发控制机制。券端点尚未进 `docs/api/openapi.yaml`，券的契约源暂为 `docs/architecture/07-coupon-pay-lifecycle.md` §5。

## 术语：成交 = 支付成功，下单不称成交

- 决策：`成交` 专指订单从 `pending_payment` 转为 `paid` 的那一刻（买家确认支付成功）；下单只称「下单成功」。
- 为什么：商城语境里成交意味着钱货两讫，下单只是提交意向。此前 `specs/checkout/spec.md` 用「成交瞬间」指下单时的快照固化点，与 `specs/order-payment/spec.md` 及 07 文档里指支付成功的用法冲突。词表新增 `成交（deal）` 词条固定此义。
- 边界：不改数据库、接口或字段名，纯文档口径。
