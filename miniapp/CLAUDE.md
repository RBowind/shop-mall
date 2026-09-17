# miniapp/ — 买家小程序

Taro 4.2.1 + React 18 + TDesign 小程序原生组件 + zustand。目标平台是微信小程序。

## 命令

```bash
pnpm test   # node --test 跑 test/*.test.mjs
pnpm build  # tsc --project tsconfig.json
```

**`pnpm build` 只跑 `tsc` 类型检查，产出的是类型门禁结果，不出小程序包。** `build:weapp` 是它的别名，走同一个 `tsc`。真正出包要直接调 Taro CLI：

```bash
cross-env TARO_APP_API_BASE=https://<微信合法域名> npx taro build --type weapp
```

这需要 `@tarojs/cli` 等 devDependencies 已就位。产物落 `dist/`，微信开发者工具以 `project.config.json` 里的 `miniprogramRoot: "dist/"` 打开。

## 目录

| 位置 | 内容 |
|---|---|
| `src/pages/` | 页面，一页一目录 |
| `src/components/` | `product-card`、`cart-item`、`order-card` |
| `src/services/` | 接口层。`generated/api.d.ts` 由 `docs/api/openapi.yaml` 生成，根目录跑 `make generate` |
| `src/services/request.ts` | 单例传输层 |
| `src/stores/` | zustand store：`auth`、`cart` |
| `src/lib/` | 纯逻辑：wx-login、checkout-session、points、errors、image-url、preferences、uuid 等 |
| `config/index.ts` | Taro CLI 配置。**在 `src/` 之外，不进 `tsc` 门禁** |

## 分包与组件注册

`src/app.config.ts` 把 tabBar 页（首页、分类、购物车、我的）留在主包，详情、结算、订单、地址、搜索、积分走 subPackages。

TDesign 是微信原生自定义组件，Taro 不打包 npm 里的原生组件，所以 `config/index.ts` 的 `copy.patterns` 把 `tdesign-miniprogram/miniprogram_dist/` 原样拷到 `dist/tdesign-miniprogram/`。`usingComponents` 在 `app.config.ts` 里全局注册一次——app.json 的路径相对 dist 根解析，页面级注册则需要 `../../` 前缀。

## 接口层与配置

`src/config.ts` 的 `API_BASE_URL` 按 `TARO_APP_API_BASE` 环境变量 → `globalThis.__API_BASE__` → `http://127.0.0.1:8080` 的顺序取值。微信要求请求地址是绝对的 `https://`，部署时必须在构建期注入真实域名。

`src/app.ts` 在模块加载时装配单例传输层并接到 auth store：请求带 Bearer token，401 回调调一次 `clearSession()`，传输层不重试，登录门不会空转。

`src/lib/taro.ts` 的 `getTaro` 从 `globalThis.Taro` 取运行时。微信环境没有这个全局，`app.ts` 在业务代码之前装入真实的 `@tarojs/taro`；测试用 `setTaroForTest` 注入假实现。

`stores/auth.ts` 的 `login()` 每次都重新取一次微信一次性 code（code 单次有效）；`clearSession()` 同时清 token、user 和持久化的结算会话。需要登录的页面在 `onShow` 里调 `requireLogin()`，它只做引导跳转不发请求。

## 测试

`test/` 下是 `.mjs` 测试文件，跑在 `node --experimental-strip-types` 上——直接 import 源码里的 `.ts`。`test/helpers.mjs` 提供公共桩件。