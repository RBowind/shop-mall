import { defineConfig } from '@umijs/max';
import zhCN from 'antd/locale/zh_CN';
import routes from './config/routes';

/**
 * Umi Max admin console.
 *
 * - hash 路由（部署到静态目录无需 rewrite，与旧的 #/products 形态一致）
 * - antd 5 中文版 + @ant-design/pro-components（ProLayout / ProTable / ProForm）
 * - plugin-access：根路由由 src/access.ts 的默认导出 accessFactory 驱动，
 *   路由上的 access 名即权限码（product:read 等），后端仍是唯一权限边界。
 * - 构建用 Umi 内置 webpack；类型门禁由 `pnpm build` 里的 `tsc --noEmit` 承担。
 *
 * metas / title 覆盖了 @umijs/server 在 SPA 模式下硬编码的默认 meta
 * （viewport 含 user-scalable=no 与 X-UA-Compatible）以满足 Lighthouse a11y/SEO。
 */
// E2E mode (ADMIN_E2E=1, set by e2e/start-dev.mjs): the dev server switches to
// https with a self-signed cert under e2e/certs, generated on demand by
// e2e/certs/generate-certs.sh (wired into `make admin-e2e`). The private key
// is never committed. The backend
// config loader only accepts https admin origins, and the admin cookies are
// Secure, so the browser-facing origin must be https for the full
// cookie/CSRF/Origin flow to run against a local backend.
const e2eHttps = process.env.ADMIN_E2E === '1' && process.env.NODE_ENV !== 'production';

export default defineConfig({
  history: { type: 'hash' },
  publicPath: process.env.NODE_ENV === 'production' ? './' : '/',
  ...(e2eHttps
    ? {
        https: {
          cert: './e2e/certs/dev-cert.pem',
          key: './e2e/certs/dev-key.pem',
        },
      }
    : {}),
  // Dev-server only (no effect on the build): same-origin /api and /static
  // proxies mirror the production nginx reverse proxy, so cookies, CSRF and
  // returned image URLs behave identically.
  proxy: {
    '/api': {
      target: `http://127.0.0.1:${process.env.ADMIN_E2E_BACKEND_PORT ?? 18080}`,
      changeOrigin: false,
    },
    '/static': {
      target: `http://127.0.0.1:${process.env.ADMIN_E2E_BACKEND_PORT ?? 18080}`,
      changeOrigin: false,
    },
  },
  title: 'Shop Mall 管理后台',
  metas: [
    { name: 'viewport', content: 'width=device-width, initial-scale=1' },
    { name: 'description', content: 'Shop Mall 管理后台：商品、订单、退款、积分、权限、会员与审计日志的统一管理入口。' },
    { name: 'theme-color', content: '#1677ff' },
  ],
  // 通过 htmlPageOpts.lang 推到 <html lang>；Umi 4 默认 SPA 输出 lang="en"。
  headScripts: [],
  antd: {
    configProvider: {
      locale: zhCN,
      theme: {
        // antd 5 default #1677ff is 4.48:1 on white — below WCAG AA 4.5:1.
        // #0958d9 lifts white-on-primary contrast above AA without changing
        // the visual brand feel much. colorLink shares the same token so
        // inline <a> links in ProTable rows don't drop below AA either.
        token: {
          colorPrimary: '#0958d9',
          colorLink: '#0958d9',
        },
      },
    },
  },
  access: {},
  model: {},
  initialState: {},
  routes,
});