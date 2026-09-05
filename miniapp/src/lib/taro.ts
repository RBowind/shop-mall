/**
 * Taro runtime shim.
 *
 * The miniapp package is compiled and tested without the Taro CLI toolchain in
 * this environment, so the small subset of Taro APIs the logic layer uses is
 * declared here. The runtime binding follows the adapter boundary used by
 * `src/services/request.ts`: logic reads the Taro object from
 * `globalThis.Taro` and tests inject a fake through `setTaroForTest`.
 *
 * The render layer (`.tsx` pages/components) imports the real
 * `@tarojs/taro` / `@tarojs/components` packages; `app.config.ts` and page
 * configs use the Taro globals `defineAppConfig` / `definePageConfig`.
 */

export interface TaroLoginResult {
  code: string;
  errMsg?: string;
}

export interface TaroLike {
  login(opts: {
    success(res: TaroLoginResult): void;
    fail?(err: unknown): void;
  }): void;
  getStorageSync(key: string): unknown;
  setStorageSync(key: string, data: unknown): void;
  removeStorageSync(key: string): void;
  navigateTo(opts: { url: string }): void;
  redirectTo(opts: { url: string }): void;
  switchTab(opts: { url: string }): void;
  navigateBack(opts?: { delta?: number }): void;
  showToast(opts: {
    title: string;
    icon?: "none" | "success" | "error" | "loading";
    duration?: number;
  }): void;
  showModal(opts: {
    title: string;
    content: string;
    showCancel?: boolean;
    confirmText?: string;
    success?(res: { confirm: boolean; cancel: boolean }): void;
    fail?(err: unknown): void;
  }): void;
  showLoading(opts: { title: string }): void;
  hideLoading(): void;
  stopPullDownRefresh?(): void;
  uploadFile?(opts: {
    url: string;
    filePath: string;
    name: string;
    header?: Record<string, string>;
    success?(res: { statusCode: number; data: string; errMsg?: string }): void;
    fail?(err: unknown): void;
  }): Promise<{ statusCode: number; data: string }> | void;
  showActionSheet?(opts: {
    itemList: string[];
    success?(res: { tapIndex: number }): void;
    fail?(err: unknown): void;
  }): void;
}

/**
 * Minimal mini-program-style component object shape. The `.tsx` render layer
 * supersedes these, but the component folders keep their event/view-model
 * contracts here for the logic tests.
 */
export interface MiniProperty {
  type: unknown;
  optionalTypes?: unknown[];
}

export interface MiniComponentData {
  [key: string]: unknown;
}

export interface MiniComponentDefinition {
  name: string;
  properties?: Record<string, MiniProperty>;
  data?: MiniComponentData;
  methods: Record<string, Function>;
}

export function getTaro(): TaroLike {
  const runtime = (globalThis as { Taro?: TaroLike }).Taro;
  if (!runtime) {
    throw new Error("Taro runtime must be supplied by the miniapp runtime");
  }
  return runtime;
}

/**
 * Install the real runtime for production. `getTaro()` reads from
 * `globalThis.Taro`; `src/app.ts` installs the real `@tarojs/taro` here so
 * stores/services run against the true API in the WeChat runtime. Tests inject
 * a fake through `setTaroForTest` instead.
 */
export function installRuntimeTaro(taro: TaroLike): void {
  (globalThis as { Taro?: TaroLike }).Taro = taro;
}

export function setTaroForTest(taro: TaroLike): void {
  (globalThis as { Taro?: TaroLike }).Taro = taro;
}