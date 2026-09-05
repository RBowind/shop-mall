import assert from "node:assert/strict";
import test from "node:test";

import { setPreferencesAdapter } from "../src/lib/preferences.ts";
import { setTaroForTest } from "../src/lib/taro.ts";
import { configureTransport } from "../src/lib/transport.ts";
import { setWxLoginCodeForTest } from "../src/lib/wx-login.ts";
import { createMiniappTransport } from "../src/services/request.ts";
import { getMe } from "../src/services/auth.ts";
import { authStore } from "../src/stores/auth.ts";
import { cartStore, isNonPurchasable } from "../src/stores/cart.ts";
import {
  cartItem,
  envelope,
  fakeTransport,
  memoryAdapter,
  offSaleProduct,
  product,
  user,
} from "./helpers.mjs";

setPreferencesAdapter(memoryAdapter());

function makeTaro() {
  const log = { toasts: 0, modals: 0, switchTabs: [] };
  const taro = {
    login(opts) {
      opts.success({ code: "test-code" });
    },
    getStorageSync() {
      return "";
    },
    setStorageSync() {},
    removeStorageSync() {},
    navigateTo() {},
    redirectTo() {},
    switchTab(opts) {
      log.switchTabs.push(opts.url);
    },
    showToast() {
      log.toasts += 1;
    },
    showModal(opts) {
      log.modals += 1;
      opts.success?.({ confirm: true, cancel: false });
    },
  };
  return { taro, log };
}

test("auth login obtains a fresh one-time code, stores token and user, and persists", async () => {
  const { taro } = makeTaro();
  setTaroForTest(taro);
  const calls = [];
  configureTransport(
    fakeTransport((input) => {
      calls.push(input);
      if (input.path === "/api/v1/auth/wx-login") {
        return { statusCode: 200, data: envelope(0, { access_token: "jwt-1", user }), header: {} };
      }
      return { statusCode: 200, data: envelope(0, user), header: {} };
    }).transport,
  );
  setWxLoginCodeForTest(async () => "fresh-code");

  await authStore.login();
  assert.equal(authStore.accessToken, "jwt-1");
  assert.equal(authStore.user.id, "9007199254740993");
  assert.equal(calls[0].path, "/api/v1/auth/wx-login");
  assert.deepEqual(calls[0].body, { code: "fresh-code" });
});

test("login gate redirects to profile without any network call and never retries", async () => {
  const { taro, log } = makeTaro();
  setTaroForTest(taro);
  const calls = [];
  configureTransport(
    fakeTransport((input) => {
      calls.push(input);
      if (input.path === "/api/v1/auth/wx-login") {
        return { statusCode: 200, data: envelope(0, { access_token: "jwt-2", user }), header: {} };
      }
      return { statusCode: 200, data: envelope(0, user), header: {} };
    }).transport,
  );

  authStore.clearSession();
  const networkBefore = calls.length;
  assert.equal(authStore.requireLogin(), false);
  assert.equal(calls.length, networkBefore);
  assert.equal(log.modals, 1);
  assert.deepEqual(log.switchTabs, ["/pages/profile/index"]);

  // Logged in: the gate opens.
  await authStore.login();
  assert.equal(authStore.requireLogin(), true);
});

test("401 clears the session exactly once and the request is never retried", async () => {
  const { taro } = makeTaro();
  setTaroForTest(taro);
  configureTransport(
    fakeTransport(() => ({
      statusCode: 200,
      data: envelope(0, { access_token: "jwt-keep", user: product }),
      header: {},
    })).transport,
  );
  await authStore.login();
  assert.ok(authStore.accessToken);

  let attempts = 0;
  configureTransport(
    createMiniappTransport({
      request: async () => {
        attempts += 1;
        return {
          statusCode: 401,
          data: { code: 1002, data: null, message: "unauthorized", trace_id: "t401" },
          header: {},
        };
      },
      getAccessToken: () => authStore.getState().accessToken,
      clearSession: () => authStore.clearSession(),
    }),
  );

  await assert.rejects(getMe(), (error) => error.statusCode === 401);
  assert.equal(attempts, 1);
  assert.equal(authStore.accessToken, null);
  assert.equal(authStore.user, null);
});

test("cart refresh updates non-purchasable flags from server status and stock", async () => {
  const { taro } = makeTaro();
  setTaroForTest(taro);
  configureTransport(
    fakeTransport(() => ({
      statusCode: 200,
      data: envelope(0, {
        list: [cartItem("11", product, 2), cartItem("12", offSaleProduct, 1)],
      }),
      header: {},
    })).transport,
  );

  await cartStore.refresh();
  const items = cartStore.getState().items;
  assert.equal(items.length, 2);
  assert.equal(isNonPurchasable(items[0]), false);
  assert.equal(isNonPurchasable(items[1]), true);

  const lowStock = { ...product, id: "9007199254740995", stock: 0, status: "on_sale" };
  assert.equal(isNonPurchasable(cartItem("13", lowStock)), true);
});

test("cart refresh keeps the server trace ID in the error state", async () => {
  const { taro } = makeTaro();
  setTaroForTest(taro);
  configureTransport(
    fakeTransport(() => ({
      statusCode: 422,
      data: { code: 2002, data: null, message: "product is off sale", trace_id: "t422" },
      header: {},
    })).transport,
  );

  await assert.rejects(cartStore.refresh());
  const error = cartStore.getState().error;
  assert.match(error.message, /off sale/);
  assert.equal(error.traceId, "t422");
});