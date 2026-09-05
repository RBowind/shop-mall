/**
 * App route and tabBar configuration.
 *
 * TabBar pages (home, category, cart, profile) must live in the main package;
 * detail, checkout and order pages are subpackages to keep the main package
 * small. TabBar icons ship later as assets; WeChat allows a text-only tabBar,
 * and the iconPath fields are optional.
 *
 * `defineAppConfig` is a Taro global (declared by `@tarojs/taro` types), not an
 * import.
 */

export default defineAppConfig({
  pages: ["pages/index/index", "pages/category/index", "pages/cart/index", "pages/profile/index"],
  subPackages: [
    { root: "pages/product-detail", pages: ["index"] },
    { root: "pages/checkout", pages: ["index"] },
    { root: "pages/orders", pages: ["index"] },
    { root: "pages/order-detail", pages: ["index"] },
    { root: "pages/address", pages: ["index", "edit"] },
    { root: "pages/search", pages: ["index"] },
    { root: "pages/points", pages: ["index"] },
  ],
  // TDesign miniprogram components (native custom components). Registered
  // GLOBALLY here on purpose: app.json paths resolve relative to the dist
  // root, where config/index.ts copies the package's miniprogram_dist.
  // Page-level usingComponents resolve relative to the page directory and
  // would need ../../ prefixes — the global entry is the single source.
  usingComponents: {
    "t-button": "tdesign-miniprogram/button/button",
    "t-cell": "tdesign-miniprogram/cell/cell",
    "t-cell-group": "tdesign-miniprogram/cell-group/cell-group",
    "t-icon": "tdesign-miniprogram/icon/icon",
    "t-tag": "tdesign-miniprogram/tag/tag",
    "t-input": "tdesign-miniprogram/input/input",
    "t-textarea": "tdesign-miniprogram/textarea/textarea",
    "t-switch": "tdesign-miniprogram/switch/switch",
    "t-empty": "tdesign-miniprogram/empty/empty",
    "t-loading": "tdesign-miniprogram/loading/loading",
    "t-stepper": "tdesign-miniprogram/stepper/stepper",
    "t-skeleton": "tdesign-miniprogram/skeleton/skeleton",
  },
  window: {
    navigationBarTitleText: "积分商城",
    navigationBarBackgroundColor: "#ffffff",
    navigationBarTextStyle: "black",
    backgroundColor: "#f5f5f5",
    backgroundTextStyle: "light",
  },
  tabBar: {
    color: "#999999",
    selectedColor: "#e64340",
    backgroundColor: "#ffffff",
    borderStyle: "black",
    list: [
      {
        pagePath: "pages/index/index",
        text: "首页",
        iconPath: "assets/tabbar/tab-home-gray.png",
        selectedIconPath: "assets/tabbar/tab-home-red.png",
      },
      {
        pagePath: "pages/category/index",
        text: "分类",
        iconPath: "assets/tabbar/tab-grid-gray.png",
        selectedIconPath: "assets/tabbar/tab-grid-red.png",
      },
      {
        pagePath: "pages/cart/index",
        text: "购物车",
        iconPath: "assets/tabbar/tab-shopping-cart-gray.png",
        selectedIconPath: "assets/tabbar/tab-shopping-cart-red.png",
      },
      {
        pagePath: "pages/profile/index",
        text: "我的",
        iconPath: "assets/tabbar/tab-user-gray.png",
        selectedIconPath: "assets/tabbar/tab-user-red.png",
      },
    ],
  },
});