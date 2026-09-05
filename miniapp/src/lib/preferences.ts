/**
 * Key-value persistence backed by Taro synchronous storage.
 *
 * A test adapter can be installed with `setPreferencesAdapter`; the default
 * adapter reads the Taro runtime from `globalThis.Taro`.
 */

import { getTaro } from "./taro.ts";

export interface PreferencesAdapter {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

const taroStorage: PreferencesAdapter = {
  getItem(key) {
    const value = getTaro().getStorageSync(key);
    return typeof value === "string" && value !== "" ? value : null;
  },
  setItem(key, value) {
    getTaro().setStorageSync(key, value);
  },
  removeItem(key) {
    getTaro().removeStorageSync(key);
  },
};

let adapter: PreferencesAdapter = taroStorage;

export function setPreferencesAdapter(next: PreferencesAdapter): void {
  adapter = next;
}

export function getPreferencesItem(key: string): string | null {
  return adapter.getItem(key);
}

export function setPreferencesItem(key: string, value: string): void {
  adapter.setItem(key, value);
}

export function removePreferencesItem(key: string): void {
  adapter.removeItem(key);
}