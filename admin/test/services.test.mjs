import assert from "node:assert/strict";
import test from "node:test";

import { configureTransport } from "../src/services/transport.ts";
import { createAdminTransport } from "../src/services/request.ts";
import {
  changePassword,
  login,
  logout,
  me,
  normalizeSession,
  withPermissions,
} from "../src/services/admin.ts";
import {
  createProduct,
  listProducts,
  updateProduct,
  uploadImage,
} from "../src/services/products.ts";
import { getOrder, listOrders, shipOrder } from "../src/services/orders.ts";
import { approveRefund, listRefunds, rejectRefund } from "../src/services/refunds.ts";
import { listAdminUsers, listRoles } from "../src/services/access.ts";
import { adjustPoints } from "../src/services/points.ts";
import {
  envelope,
  fakeTransport,
  ledgerEntry,
  operatorLogin,
  order,
  product,
  roles,
  superAdminLogin,
} from "./helpers.mjs";

function install(respond) {
  const { transport, calls } = fakeTransport(respond);
  configureTransport(transport);
  return calls;
}

test("login posts to /api/admin/v1/auth/login and normalizes the contract role object", async () => {
  const calls = install(() => superAdminLogin());
  const session = await login("root", "long-enough-password");
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].path, "/api/admin/v1/auth/login");
  assert.deepEqual(calls[0].body, { username: "root", password: "long-enough-password" });
  assert.equal(session.username, "root");
  assert.equal(session.roleName, "super_admin");
  assert.ok(session.permissions.includes("product:write"));
  assert.ok(session.permissions.includes("role:manage"));
});

test("login tolerates the backend reality where role is a role-name string", async () => {
  const calls = install(() => operatorLogin());
  const session = await login("operator", "long-enough-password");
  assert.equal(session.roleName, "operator");
  assert.equal(session.permissions.length, 0);
  assert.equal(calls[0].path, "/api/admin/v1/auth/login");
});

test("logout and changePassword hit the auth endpoints with the contract body", async () => {
  const calls = install(() => envelope(0, null));
  await logout();
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].path, "/api/admin/v1/auth/logout");

  await changePassword("old-pass", "new-long-password");
  assert.equal(calls[1].method, "POST");
  assert.equal(calls[1].path, "/api/admin/v1/auth/password");
  assert.deepEqual(calls[1].body, {
    current_password: "old-pass",
    new_password: "new-long-password",
  });
});

test("me reads the current profile from /api/admin/v1/auth/me", async () => {
  const calls = install(() => envelope(0, { username: "root", role: "super_admin", enabled: true }));
  const session = await me();
  assert.equal(calls[0].method, "GET");
  assert.equal(calls[0].path, "/api/admin/v1/auth/me");
  assert.equal(session.username, "root");
  assert.equal(session.enabled, true);
});

test("withPermissions widens an empty permission set from the roles endpoint", async () => {
  const base = normalizeSession(operatorLogin().data);
  const widened = await withPermissions(base, async () => roles);
  assert.deepEqual(widened.permissions, roles[1].permissions);
});

test("withPermissions keeps the session unchanged when roles cannot be fetched", async () => {
  const base = normalizeSession(operatorLogin().data);
  const widened = await withPermissions(base, async () => {
    throw new Error("forbidden");
  });
  assert.equal(widened.permissions.length, 0);
});

test("products service maps list/create/update/upload onto the contract", async () => {
  const calls = install((input) => {
    if (input.path === "/api/admin/v1/products" && input.method === "GET") {
      return envelope(0, { list: [product], total: 1, page: 1, page_size: 20 });
    }
    if (input.path === "/api/admin/v1/products" && input.method === "POST") {
      return envelope(0, product);
    }
    return envelope(0, product);
  });

  const page = await listProducts({ page: 1, page_size: 20, status: "on_sale" });
  assert.equal(page.list[0].id, "9007199254740993");
  assert.equal(page.total, 1);
  assert.equal(calls[0].path, "/api/admin/v1/products");
  assert.equal(calls[0].query.status, "on_sale");

  const created = await createProduct({
    name: "新商品",
    price_points: "100",
    stock: 3,
    main_image: "images/abc.webp",
  });
  assert.equal(created.name, "测试商品");
  const createCall = calls[1];
  assert.equal(createCall.method, "POST");
  assert.deepEqual(createCall.body, {
    name: "新商品",
    price_points: "100",
    stock: 3,
    main_image: "images/abc.webp",
  });

  const updated = await updateProduct("9007199254740993", {
    name: "改名",
    price_points: "120",
    stock: 4,
    status: "off_sale",
  });
  assert.equal(updated.status, "on_sale");
  assert.equal(calls[2].method, "PATCH");
  assert.equal(calls[2].path, "/api/admin/v1/products/9007199254740993");
});

test("uploadImage posts multipart with a file field and returns the image key", async () => {
  const calls = install(() =>
    envelope(0, { key: "images/new.webp", url: "https://img.example.test/new.webp", width: 100, height: 100, size: 1024 }),
  );
  const file = new File(["bytes"], "new.webp", { type: "image/webp" });
  const result = await uploadImage(file);
  assert.equal(result.key, "images/new.webp");
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].path, "/api/admin/v1/images");
  assert.ok(calls[0].body instanceof FormData);
  assert.equal(calls[0].body.get("file"), file);
});

test("orders service maps list/get/ship onto the contract", async () => {
  const calls = install((input) => {
    if (input.path === "/api/admin/v1/orders" && input.method === "GET") {
      return envelope(0, { list: [order], total: 1, page: 1, page_size: 20 });
    }
    return envelope(0, order);
  });

  await listOrders({ page: 1, page_size: 10, status: "paid" });
  assert.equal(calls[0].path, "/api/admin/v1/orders");
  assert.equal(calls[0].query.status, "paid");

  const detail = await getOrder("9007199254741001");
  assert.equal(detail.order_no, "ORD1-abc");
  assert.equal(calls[1].path, "/api/admin/v1/orders/9007199254741001");

  await shipOrder("9007199254741001");
  assert.equal(calls[2].method, "POST");
  assert.equal(calls[2].path, "/api/admin/v1/orders/9007199254741001/ship");
});

test("refunds service maps list/approve/reject onto the contract", async () => {
  const calls = install((input) => {
    if (input.path === "/api/admin/v1/refunds" && input.method === "GET") {
      return envelope(0, { list: [{ ...order, status: "refund_requested", refund_reason: "不想要了" }], total: 1, page: 1, page_size: 20 });
    }
    return envelope(0, order);
  });

  const page = await listRefunds({ page: 1, page_size: 20 });
  assert.equal(page.list[0].status, "refund_requested");
  assert.equal(calls[0].path, "/api/admin/v1/refunds");

  await approveRefund("9007199254741001");
  assert.equal(calls[1].method, "POST");
  assert.equal(calls[1].path, "/api/admin/v1/refunds/9007199254741001/approve");

  await rejectRefund("9007199254741001", "商品完好");
  assert.equal(calls[2].method, "POST");
  assert.equal(calls[2].path, "/api/admin/v1/refunds/9007199254741001/reject");
  assert.deepEqual(calls[2].body, { reason: "商品完好" });
});

test("access service lists roles and administrator accounts", async () => {
  const calls = install((input) => {
    if (input.path === "/api/admin/v1/roles") {
      return envelope(0, { list: roles });
    }
    return envelope(0, {
      list: [{ id: "1", username: "root", enabled: true, role: roles[0] }],
      total: 1,
      page: 1,
      page_size: 20,
    });
  });

  const roleList = await listRoles();
  assert.equal(roleList.length, 2);
  assert.equal(calls[0].path, "/api/admin/v1/roles");

  const users = await listAdminUsers({ page: 1, page_size: 20 });
  assert.equal(users.list[0].username, "root");
  assert.equal(calls[1].path, "/api/admin/v1/admin-users");
  assert.equal(calls[1].query.page_size, 20);
});

test("adjustPoints sends an Idempotency-Key and the mandatory remark", async () => {
  const calls = install(() => envelope(0, ledgerEntry));
  const entry = await adjustPoints(
    { user_id: "9007199254740993", delta: "-100", remark: "活动补偿" },
    "00000000-0000-0000-0000-000000000000",
  );
  assert.equal(entry.delta, "-100");
  assert.equal(entry.balance_after, "900");
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].path, "/api/admin/v1/points/adjust");
  assert.equal(calls[0].headers["Idempotency-Key"], "00000000-0000-0000-0000-000000000000");
  assert.deepEqual(calls[0].body, {
    user_id: "9007199254740993",
    delta: "-100",
    remark: "活动补偿",
  });
});

test("adjustPoints generates a fresh idempotency key when none is supplied", async () => {
  const calls = install(() => envelope(0, ledgerEntry));
  await adjustPoints({ user_id: "1", delta: "10", remark: "手动赠送" });
  const key = calls[0].headers["Idempotency-Key"];
  assert.ok(key && key.length >= 20);
});

test("a write through the real transport carries the CSRF header from the cookie", async () => {
  const received = [];
  const transport = createAdminTransport({
    baseUrl: "https://admin.example.test",
    getCookie: () => "csrf_token=csrf-value; theme=dark",
    fetch: async (_url, init) => {
      received.push(init);
      return new Response(
        JSON.stringify(envelope(0, { ...product, name: "带CSRF" })),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    },
  });
  configureTransport(transport);

  await createProduct({ name: "带CSRF", price_points: "100", stock: 1 });
  assert.equal(received[0].credentials, "include");
  assert.equal(received[0].headers["X-CSRF-Token"], "csrf-value");
  assert.equal(received[0].headers["Content-Type"], "application/json");
});