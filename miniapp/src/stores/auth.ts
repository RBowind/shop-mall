/**
 * Buyer authentication store.
 *
 * Exposes `accessToken`, `user`, `clearSession()` and `login()` per the task
 * contract, backed by Taro synchronous storage. `login()` always obtains a
 * fresh one-time WeChat code before calling wx-login (codes are single-use).
 * `clearSession()` drops the token, the user and the persisted checkout
 * session so a second login starts clean.
 *
 * Login gating: pages that require a session call `requireLogin()` in their
 * `onShow`. It never performs a network call and never retries; when unauthenticated
 * it prompts and switches to the profile tab where the login button lives. The
 * transport's 401 callback calls `clearSession()`, so the gating loop cannot spin.
 */

import { createStore } from "zustand/vanilla";

import { clearCheckoutSession } from "../lib/checkout-session.ts";
import {
  getPreferencesItem,
  removePreferencesItem,
  setPreferencesItem,
} from "../lib/preferences.ts";
import { getTaro } from "../lib/taro.ts";
import { getWxLoginCode } from "../lib/wx-login.ts";
import { wxLogin } from "../services/auth.ts";
import type { User } from "../services/types.ts";

export interface AuthState {
  accessToken: string | null;
  user: User | null;
}

const AUTH_TOKEN_KEY = "auth.access_token";
const AUTH_USER_KEY = "auth.user";

const store = createStore<AuthState>(() => ({
  accessToken: null,
  user: null,
}));

export interface AuthStore {
  readonly accessToken: string | null;
  readonly user: User | null;
  getState(): AuthState;
  subscribe(listener: () => void): () => void;
  restore(): void;
  login(): Promise<User>;
  applyUser(user: User): void;
  clearSession(): void;
  requireLogin(): boolean;
}

export const authStore: AuthStore = {
  get accessToken() {
    return store.getState().accessToken;
  },
  get user() {
    return store.getState().user;
  },
  getState: store.getState,
  subscribe: store.subscribe,

  restore() {
    const token = getPreferencesItem(AUTH_TOKEN_KEY);
    if (!token) return;
    const rawUser = getPreferencesItem(AUTH_USER_KEY);
    let user: User | null = null;
    if (rawUser) {
      try {
        user = JSON.parse(rawUser) as User;
      } catch {
        user = null;
      }
    }
    store.setState({ accessToken: token, user });
  },

  async login() {
    const code = await getWxLoginCode();
    const result = await wxLogin(code);
    store.setState({ accessToken: result.accessToken, user: result.user });
    setPreferencesItem(AUTH_TOKEN_KEY, result.accessToken);
    setPreferencesItem(AUTH_USER_KEY, JSON.stringify(result.user));
    return result.user;
  },

  applyUser(user: User) {
    store.setState({ user });
    setPreferencesItem(AUTH_USER_KEY, JSON.stringify(user));
  },

  clearSession() {
    store.setState({ accessToken: null, user: null });
    removePreferencesItem(AUTH_TOKEN_KEY);
    removePreferencesItem(AUTH_USER_KEY);
    clearCheckoutSession();
  },

  requireLogin() {
    if (store.getState().accessToken) return true;
    getTaro().showModal({
      title: "需要登录",
      content: "登录后即可继续使用积分下单",
      showCancel: false,
      confirmText: "去登录",
      success() {
        getTaro().switchTab({ url: "/pages/profile/index" });
      },
    });
    return false;
  },
};