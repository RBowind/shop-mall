/**
 * Taro CLI configuration (used by `taro build --type weapp`).
 *
 * This file is outside `src/` and is NOT type-checked by the `tsc` gate; it is
 * consumed only by the Taro CLI. To run a real build you need the Taro
 * devDependencies (`@tarojs/cli`, `@tarojs/plugin-framework-react`,
 * `babel-preset-taro`, `@tarojs/webpack5-runner`, `cross-env`) and then:
 *
 *   cross-env TARO_APP_API_BASE=https://<wechat-legal-domain> \
 *     npx taro build --type weapp
 *
 * The WeChat devtools opens `project.config.json` with `dist/` as the
 * mini-program root. Use `touristappid` locally; the real AppID lives only in
 * the deployment environment.
 */

import { defineConfig } from "@tarojs/cli";

export default defineConfig({
  projectName: "shop-mall-miniapp",
  date: "2026-08-12",
  designWidth: 750,
  deviceRatio: {
    640: 2.34 / 2,
    750: 1,
    375: 2,
    828: 1.81 / 2,
  },
  sourceRoot: "src",
  outputRoot: "dist",
  plugins: ["@tarojs/plugin-framework-react"],
  defineConstants: {},
  copy: {
    patterns: [
      {
        // TDesign components are native WeChat custom components (consumed
        // via app.config.ts usingComponents with npm package paths). Taro
        // does not bundle npm native components, so the package's
        // miniprogram_dist is copied verbatim to dist/<pkg>, which is where
        // the usingComponents paths resolve at runtime.
        from: "node_modules/tdesign-miniprogram/miniprogram_dist/",
        to: "dist/tdesign-miniprogram/",
        ignore: ["*.ts"],
      },
    ],
    options: {},
  },
  framework: "react",
  compiler: "webpack5",
  mini: {
    postcss: {
      pxtransform: { enable: true, config: {} },
      url: { enable: true, config: { limit: 1024 } },
      cssModules: {
        enable: false,
        config: {
          namingPattern: "module",
          generateScopedName: "[name]__[local]___[hash:base64:5]",
        },
      },
    },
  },
  h5: {
    publicPath: "/",
    staticDirectory: "static",
    postcss: {
      autoprefixer: { enable: true, config: {} },
      cssModules: {
        enable: false,
        config: {
          namingPattern: "module",
          generateScopedName: "[name]__[local]___[hash:base64:5]",
        },
      },
    },
  },
});