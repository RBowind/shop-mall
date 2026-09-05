/**
 * One-time WeChat login code acquisition.
 *
 * WeChat's `code` is single-use. Every wx-login attempt must call `wx.login`
 * again for a fresh code; a stale code must never be replayed. `session_key`
 * is never exposed to or persisted by the miniapp.
 */

import { getTaro } from "./taro.ts";

let codeFactory: (() => Promise<string>) | null = null;

export function setWxLoginCodeForTest(factory: () => Promise<string>): void {
  codeFactory = factory;
}

export function resetWxLoginCodeForTest(): void {
  codeFactory = null;
}

export function getWxLoginCode(): Promise<string> {
  if (codeFactory) {
    return codeFactory();
  }
  return new Promise((resolve, reject) => {
    getTaro().login({
      success(result) {
        if (result.code) {
          resolve(result.code);
        } else {
          reject(new Error("wx.login returned no code"));
        }
      },
      fail(err) {
        reject(err instanceof Error ? err : new Error("wx.login failed"));
      },
    });
  });
}