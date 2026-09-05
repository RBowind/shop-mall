import assert from "node:assert/strict";
import test from "node:test";

import { setPreferencesAdapter } from "../src/lib/preferences.ts";
import { configureTransport } from "../src/lib/transport.ts";
import { setTaroForTest } from "../src/lib/taro.ts";
import { listAddresses } from "../src/services/addresses.ts";
import { getMe, updateMe, uploadAvatar, wxLogin } from "../src/services/auth.ts";
import {
  addCartItem,
  deleteCartItem,
  getCart,
  updateCartItem,
} from "../src/services/cart.ts";
import {
  confirmOrder,
  createOrder,
  getOrder,
  listOrders,
  requestRefund,
} from "../src/services/orders.ts";
import {
  getProduct,
  listCategories,
  listProducts,
} from "../src/services/products.ts";
import { address, cartItem, envelope, fakeTransport, order, product, user } from "./helpers.mjs";

// Isolate the checkout session used by createOrder.
setPreferencesAdapter({
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
});

function install(respond) {
  const { transport, calls } = fakeTransport(respond);
  configureTransport(transport);
  return calls;
}

test("wxLogin posts the one-time code to /api/v1/auth/wx-login", async () => {
  const calls = install(() =>
    envelope(0, { access_token: "jwt-token", user: product }),
  );
  const result = await wxLogin("one-time-code");
  assert.equal(result.accessToken, "jwt-token");
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].path, "/api/v1/auth/wx-login");
  assert.deepEqual(calls[0].body, { code: "one-time-code" });
});

test("getMe calls /api/v1/me", async () => {
  const calls = install(() => envelope(0, user));
  const me = await getMe();
  assert.equal(me.points_balance, "1000");
  assert.equal(calls[0].method, "GET");
  assert.equal(calls[0].path, "/api/v1/me");
});

test("updateMe PATCHes only the present fields and returns the fresh profile", async () => {
  const calls = install(() => envelope(0, { ...user, nickname: "新昵称" }));
  const updated = await updateMe({ nickname: "新昵称" });
  assert.equal(updated.nickname, "新昵称");
  assert.equal(calls[0].method, "PATCH");
  assert.equal(calls[0].path, "/api/v1/me");
  assert.deepEqual(calls[0].body, { nickname: "新昵称" });
});

test("uploadAvatar posts the temp file with the bearer token and returns the stored URL", async () => {
  let uploadArgs = null;
  setTaroForTest({
    uploadFile(opts) {
      uploadArgs = opts;
      opts.success?.({ statusCode: 201, data: JSON.stringify(envelope(0, { url: "https://img.example.test/avatar.jpg" })) });
      return Promise.resolve({ statusCode: 201, data: JSON.stringify(envelope(0, { url: "https://img.example.test/avatar.jpg" })) });
    },
  });
  const url = await uploadAvatar("wxfile://tmp/avatar.jpg", "jwt-token");
  assert.equal(url, "https://img.example.test/avatar.jpg");
  assert.equal(uploadArgs.url, "http://127.0.0.1:8080/api/v1/me/avatar");
  assert.equal(uploadArgs.filePath, "wxfile://tmp/avatar.jpg");
  assert.equal(uploadArgs.name, "file");
  assert.equal(uploadArgs.header.Authorization, "Bearer jwt-token");
});

test("uploadAvatar surfaces the server error message on failure", async () => {
  setTaroForTest({
    uploadFile(opts) {
      opts.success?.({ statusCode: 413, data: JSON.stringify({ code: 1013, data: null, message: "file exceeds upload limit", trace_id: "t" }) });
      return Promise.resolve({ statusCode: 413, data: JSON.stringify({ code: 1013, data: null, message: "file exceeds upload limit", trace_id: "t" }) });
    },
  });
  await assert.rejects(uploadAvatar("wxfile://tmp/big.jpg", "jwt-token"), /file exceeds upload limit/);
});

test("products service keeps int64 ids as strings and passes query params", async () => {
  const calls = install((input) =>
    input.path === "/api/v1/products"
      ? envelope(0, { list: [product], total: 1, page: 1, page_size: 20 })
      : envelope(0, product),
  );
  const detail = await getProduct("9007199254740993");
  assert.equal(detail.id, "9007199254740993");
  assert.equal(calls[0].method, "GET");
  assert.equal(calls[0].path, "/api/v1/products/9007199254740993");

  const result = await listProducts({ page: 1, page_size: 20 });
  assert.equal(result.list.length, 1);
  assert.equal(result.list[0].price_points, "100");
  assert.equal(result.total, 1);
  assert.equal(calls[1].path, "/api/v1/products");
  assert.equal(calls[1].query.page, 1);
  assert.equal(calls[1].query.page_size, 20);
});

test("listCategories unwraps the envelope into an array", async () => {
  const calls = install(() =>
    envelope(0, [
      { id: "digital", name: "数码", product_count: 6 },
      { id: "food", name: "食品", product_count: 4 },
    ]),
  );
  const categories = await listCategories();
  assert.equal(categories.length, 2);
  assert.equal(categories[0].id, "digital");
  assert.equal(categories[0].product_count, 6);
  assert.equal(calls[0].method, "GET");
  assert.equal(calls[0].path, "/api/v1/categories");
});

test("cart service maps CRUD operations onto the contract", async () => {
  const calls = install((input) => {
    if (input.method === "POST") return envelope(0, cartItem("11", product));
    if (input.method === "PATCH") return envelope(0, cartItem("11", product, 3));
    if (input.method === "DELETE") {
      return envelope(0, null);
    }
    return envelope(0, { list: [cartItem("11", product)] });
  });

  await getCart();
  assert.equal(calls[0].method, "GET");
  assert.equal(calls[0].path, "/api/v1/cart");

  await addCartItem("9007199254740993", 1);
  assert.equal(calls[1].method, "POST");
  assert.equal(calls[1].path, "/api/v1/cart");
  assert.deepEqual(calls[1].body, {
    product_id: "9007199254740993",
    quantity: 1,
  });

  await updateCartItem("11", 3);
  assert.equal(calls[2].method, "PATCH");
  assert.equal(calls[2].path, "/api/v1/cart/11");
  assert.deepEqual(calls[2].body, { quantity: 3 });

  await deleteCartItem("11");
  assert.equal(calls[3].method, "DELETE");
  assert.equal(calls[3].path, "/api/v1/cart/11");
});

test("addresses service lists buyer addresses", async () => {
  const calls = install(() => envelope(0, { list: [address] }));
  const list = await listAddresses();
  assert.equal(list.length, 1);
  assert.equal(list[0].version, "1");
  assert.equal(calls[0].path, "/api/v1/addresses");
});

test("createOrder sends Idempotency-Key and only cart item ids + address id", async () => {
  const calls = install(() => envelope(0, order));
  const created = await createOrder({
    lines: [
      { id: "11", quantity: 2 },
      { id: "12", quantity: 1 },
    ],
    addressId: "7",
    addressVersion: "1",
  });
  assert.equal(created.id, "9007199254741001");
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].path, "/api/v1/orders");
  assert.ok(calls[0].headers["Idempotency-Key"]);
  assert.equal(calls[0].headers["Idempotency-Key"].length, 36);
  assert.deepEqual(calls[0].body, {
    cart_item_ids: ["11", "12"],
    address_id: "7",
  });
});

test("order queries and actions map onto the contract", async () => {
  const calls = install((input) => envelope(0, order));

  await listOrders({ page: 1, page_size: 10, status: "paid" });
  assert.equal(calls[0].path, "/api/v1/orders");
  assert.equal(calls[0].query.status, "paid");

  await getOrder("9007199254741001");
  assert.equal(calls[1].path, "/api/v1/orders/9007199254741001");

  await requestRefund("9007199254741001", "不想要了");
  assert.equal(calls[2].method, "POST");
  assert.equal(calls[2].path, "/api/v1/orders/9007199254741001/refund");
  assert.deepEqual(calls[2].body, { reason: "不想要了" });

  await confirmOrder("9007199254741001");
  assert.equal(calls[3].method, "POST");
  assert.equal(calls[3].path, "/api/v1/orders/9007199254741001/confirm");
});