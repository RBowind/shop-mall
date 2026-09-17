# backend/ — Go 单体后端

Gin + GORM + PostgreSQL 16，迁移用 goose。买家与管理员共用一套进程，靠两套 JWT realm 区分。

## 命令

```bash
make test    # go test ./...
make lint    # gofmt -l，报错即要求 gofmt -w
make build   # go build ./...
```

在仓库根目录用 `make -C backend test` 等价。CI 额外跑 `go test -race ./...` 与 `govulncheck`，本地改并发代码时手动带 `-race`。

需要真库的验收门在根 Makefile：`make test-e2e`、`make test-security`、`make test-concurrency`。

## 分层

| 层 | 位置 | 职责 |
|---|---|---|
| 组合根 | `cmd/server/main.go` | `buildRouter()` 手工装配全部依赖 |
| 领域包 | `internal/{cart,order,product,user,admin,storage}` | 每包一套 `register.go` / `handler.go` / `service.go` / `repository.go` |
| 积分账本 | `internal/payment/` | `balance.go` + `ledger.go` + 仓储，无 HTTP 面，由 order、user 领域调用 |
| 用例层 | `internal/application/{access,auth,order,points,refund}` | 跨领域编排：登录、下单、发货、退款、积分调整 |
| 基础设施 | `internal/platform/*` | database、http(router/response)、tokens、wechat、audit、metrics、logging、uid |
| 中间件 | `internal/middleware/` | auth、permission、csrf、rate_limit、recover、trace |
| 命令行 | `cmd/admin-bootstrap/` | 建管理员、发角色权限的独立二进制 |

`internal/application/order` 是下单编排，`internal/order` 是该领域的读模型与仓储——同名不同层，改动前先看清在哪一层。

## 主键与 ID

所有资源主键是 `uid.ID`（`internal/platform/uid`），底层是 UUIDv7 别名。**ID 由 service 层在 INSERT 前生成**，数据库侧没有 default；UUIDv7 保证 `ORDER BY id DESC` 仍是最新在前，兼容旧 BIGSERIAL 索引的语义。完整口径见 `docs/architecture/08-uuid-primary-keys.md`。

GORM 对自定义标量类型走 `Scan` 会丢值，跨表聚合查询用 `db.Raw(...).Row().Scan(...)` 逐列取。

## 迁移

`migrations/NNNN_name.up.sql`，goose 格式。`main.go` 启动时调 `database.RunMigrations(ctx, db, "migrations")`，路径是相对当前工作目录的——**从 `backend/` 目录启动进程**，否则找不到迁移目录。

`database.go` 会自动把 plpgsql 函数体包进 `-- +goose StatementBegin/End`，写带 `$$` 的函数迁移时按普通 SQL 写即可。

## 测试

| 目录 | 内容 | 数据库 |
|---|---|---|
| `tests/e2e/` | 全量验收，`harness.go` 起真实 TLS httptest 服务 | 真库，无库则 skip |
| `tests/integration/` | 下单并发、积分扣减竞态 | 真库 |
| `tests/security/` | IDOR、管理员 403/422、CSRF/Origin、上传绕过 | 真库 |
| `tests/fixtures/` | 测试数据 | — |

`harness.go` 复刻 `cmd/server/main.go` 的装配，只替换微信客户端（把冻结的 code 映射到冻结的 openid）。**改组合根时同步改 harness**，否则验收测试跑的不是生产接线。

数据库来源：设 `TEST_DATABASE_URL` 指向现成实例，或留空让 harness 自行拉起 Docker 容器。

验收测试命名 `TestSpec<Capability><Bn>_<Desc>`，每 capability 一个 `<capability>_spec_test.go`，文件头放 B↔用例映射表。

## 配置

全部经环境变量，读入口 `internal/config/config.go`。

- 数据库：`DATABASE_URL`、`DB_MAX_OPEN_CONNS`、`DB_MAX_IDLE_CONNS`、`DB_CONN_MAX_LIFETIME`、`DB_CONN_MAX_IDLE_TIME`
- 买家 JWT：`BUYER_JWT_KEYRING`、`_ACTIVE_KID`、`_ISSUER`、`_AUDIENCE`、`_TTL`
- 管理员 JWT：`ADMIN_JWT_*`（同上五项）、`ADMIN_ALLOWED_ORIGINS`
- 微信：`WECHAT_APP_ID`、`WECHAT_APP_SECRET`、`WECHAT_ENDPOINT`
- 图片：`IMAGE_VOLUME_DIR`、`IMAGE_MAX_PIXELS`、`IMAGE_STORAGE_CAPACITY_BYTES`、`IMAGE_CLEANUP_INTERVAL`、`IMAGE_GRACE_PERIOD`、`UPLOAD_MAX_BYTES`
- 其它：`HTTP_ADDR`、`APP_ENV`、`PUBLIC_BASE_URL`、`SIGNUP_BONUS_POINTS`

两套 JWT 各有独立 keyring 与 issuer，中间件按 realm 分开校验——给买家签的 token 用在管理接口上会被拒。

## 审计

`internal/platform/audit` 是 append-only 审计写入口，退款审核、发货、积分调整、权限变更都经它落账。金额整数与日志脱敏的口径见根 AGENTS.md「工作约定」。