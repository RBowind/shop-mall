# admin/ — 管理后台 SPA

Umi Max 4.7 + React 18 + antd 5 + `@ant-design/pro-components`。路由与菜单自建：`config/routes.ts` 从 `src/menu.ts` 派生。hash 路由，部署到静态目录无需 rewrite。

## 命令

```bash
pnpm dev      # max dev → http://localhost:8000
pnpm build    # tsc --noEmit && max build && node scripts/patch-html.mjs
pnpm test     # node --test 跑 test/*.test.mjs
pnpm test:e2e # Playwright，见下
```

`build` 三段串联：`tsc --noEmit` 是类型门禁（Umi 构建本身不做类型检查），`max build` 产出 `dist/`，`patch-html.mjs` 收尾改写 `index.html`——Umi 4 的 SPA 模板会忽略 `htmlPageOpts.lang`，`<html lang="zh-CN">` 只能构建后补，否则 Lighthouse `html-has-lang` 直接红。补丁幂等，重复跑是无操作。

## 权限与路由

权限码在 `src/access.ts` 的 `PERMISSIONS` 常量里，与 `backend/migrations/0002_seed.up.sql`、`0003_add_admin_read_permissions.up.sql` 及 openapi 的 `x-required-permission` 对齐。

`src/menu.ts` 的 `MENU_ITEMS` 是侧边栏唯一真源（路径 + 名称 + 所需权限码），`config/routes.ts` 由它派生 Umi 路由表并补上 `component` 与 `access` 字段。新增页面按这个链条加：`menu.ts` → `pages/<name>/`，路由与菜单自动跟上。

前端权限只驱动菜单渲染与直接访问时的拒绝页，后端是唯一安全边界。

## 接口层

- `src/services/generated/api.d.ts` 由 `docs/api/openapi.yaml` 生成，根目录跑 `make generate`。手改会在 `make contract-check` 时暴露。
- `src/services/transport.ts` + `request.ts` 是单例传输层，在 `src/app.tsx` 模块加载时装配一次，Cookie、credentials、CSRF、401 跳登录的逻辑都在这里。
- 各业务服务文件（`orders.ts`、`products.ts`、`refunds.ts` …）建在传输层之上。
- `src/lib/idem.ts` 管幂等键，`src/lib/image-compress.ts` 在上传前做客户端压缩。

`src/app.tsx` 的 `getInitialState` 拉 `/auth/me` 建会话；未登录或 Cookie 过期时降级为 `{ session: null }`，所有权限位变假并跳登录页。

## 访问地址

同一个后台三张面孔，地址与托管方各不相同：

| 地址 | 谁在服务 | 何时可用 |
|---|---|---|
| `http://localhost:8000` | `pnpm dev` 起的 Umi dev server，明文 http | 手动开发时 |
| `https://localhost:8000` | 同一个 dev server，`ADMIN_E2E=1` 时切自签 https | 仅 `make admin-e2e` 运行期间 |
| `https://localhost` | compose 的 nginx，托管 `admin/dist` | `make compose-up` 之后 |

`https://localhost:8000` 由 Playwright 的 webServer 拉起（`e2e/start-dev.mjs`），命令结束即消失，平时打开是连接失败——那是 e2e 的临时地址，不是后台的常驻入口。

`.umirc.ts` 的 dev 代理把 `/api` 与 `/static` 指到 `127.0.0.1:18080`（用 `ADMIN_E2E_BACKEND_PORT` 可覆盖），那是 e2e 后端端口；compose 的后端在 8080。所以单独跑 `pnpm dev` 时还需要一个监听 18080 的后端，`/api` 才通。同源代理让 Cookie、CSRF、返回的图片 URL 在本地与生产表现一致。

## E2E

Playwright 套件在 `e2e/`，用根 Makefile 一条命令跑：

```bash
make admin-e2e       # 首次运行前需 pnpm --filter admin exec playwright install chromium
make admin-e2e-down  # 清掉 PG 容器
```

这条链路会：生成自签证书 → 拉起一次性 PostgreSQL 容器（tmpfs 数据目录）→ 把 `server` 与 `admin-bootstrap` 编到 `e2e/.build/` → 跑 Playwright。

`e2e/global-setup.mjs` 在每次运行前重置数据库（关闭审计触发器再 DELETE，因为 `audit_logs` 是 append-only），然后用真实 HTTP 接口种下 superadmin、viewer、商品和一笔已付款订单，状态写进 `e2e/.build/seed-state.json`。

e2e 运行时浏览器访问 `https://localhost:8000`（dev server 切自签 https），后端在 `127.0.0.1:18080`，PG 在 `127.0.0.1:15432`。端口都可用 `ADMIN_E2E_*` 环境变量覆盖。

后端配置只接受 https 的管理端 Origin，admin 的 Cookie 又带 `Secure`，所以浏览器侧必须是 https 才能跑通完整的 Cookie/CSRF/Origin 流程。Playwright 单 worker 串行执行，因为各 spec 共享并修改同一份种子状态。