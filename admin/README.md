# admin

零框架的纯 TypeScript + 手写 DOM 管理后台 SPA（无 React/AntD/Vite）。

## 命令

| 命令 | 作用 |
| --- | --- |
| `pnpm build` | `tsc --noEmit` 类型检查 gate（无产物） |
| `pnpm build:browser` | esbuild 构建浏览器 bundle 到 `dist/`（含 index.html、admin.js、admin.css） |
| `pnpm test` | Node 内置测试运行器跑 38 个单测 |
| `pnpm serve` | 极简静态服务器，预览 `dist/`（默认 http://localhost:4173） |

## 本地预览登录页

```bash
cd shop-mall/admin
pnpm install
pnpm build:browser   # 产出 dist/admin.js + dist/admin.css + dist/index.html
pnpm serve           # 打开 http://localhost:4173/
```

也可以随便用一个静态服务器指向 `dist/`：

```bash
npx serve dist
# 或
npx http-server dist -p 8080
```

未登录访问会走到 hash 路由 `#/login`，渲染登录表单（即使 API 不通也会显示登录页）。

## 与后端联调

默认 API base 走同源相对路径（`/api`），正式部署由静态服务器同源代理 `/api` 到 Go 后端。本地开发可用 `window.SHOP_MALL_ADMIN_API_BASE` 覆盖 API 地址，例如在浏览器控制台执行：

```js
window.SHOP_MALL_ADMIN_API_BASE = "https://your-backend.example";
location.reload();
```

CsrfToken/session cookie 由 transport 读取并在写请求上注入 `X-CSRF-Token`，跨源联调时需要后端允许 admin 来源（CORS）并接受 cookie 携带。

## dist/ 结构

```
dist/
  index.html     # 静态入口（引用同目录 admin.js / admin.css）
  admin.js       # esbuild 打包产物（ES module，es2022）
  admin.css      # 从 public/admin.css 复制的样式
```

`dist/` 是 gitignore 的构建产物，改样式改 `public/admin.css`，改入口改根目录 `index.html`。