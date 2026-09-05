/**
 * Shared admin test helpers: a recording fake transport that mimics the real
 * `createAdminTransport` behavior (wraps a plain envelope in a 200 response
 * and throws `ApiRequestError` for every non-2xx status) plus envelope
 * fixtures for the admin domain.
 */

import { ApiRequestError } from "../src/services/request.ts";

/**
 * Builds a transport that records every request. `respond` returns either a
 * plain envelope (turned into a 200) or a `{ statusCode, data, headers }`
 * object so tests can simulate 403/409/422/500 responses.
 */
export function fakeTransport(respond, calls = []) {
  const transport = async (input) => {
    calls.push(input);
    const result = await respond(input, calls);
    const response =
      result !== null && typeof result === "object" && "statusCode" in result
        ? result
        : { statusCode: 200, data: result, headers: {} };
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw new ApiRequestError(response);
    }
    return response;
  };
  return { transport, calls };
}

export function envelope(code, data, traceId = "trace-fixed") {
  return { code, data, message: "ok", trace_id: traceId };
}

export const superAdminLogin = () =>
  envelope(0, {
    admin_id: "1",
    username: "root",
    role: {
      id: "1",
      name: "super_admin",
      permissions: [
        "product:read",
        "product:write",
        "image:write",
        "order:read",
        "order:ship",
        "refund:read",
        "refund:approve",
        "points:adjust",
        "role:manage",
        "admin:self",
      ],
    },
  });

/** Backend-reality login shape: role is a plain role-name string. */
export const operatorLogin = () =>
  envelope(0, {
    admin_id: "2",
    username: "operator",
    role: "operator",
    token_version: 1,
  });

export const operatorPermissions = [
  "product:read",
  "product:write",
  "image:write",
  "order:read",
  "order:ship",
  "refund:read",
  "admin:self",
];

export const product = {
  id: "9007199254740993",
  name: "测试商品",
  description: "desc",
  main_image: "images/abc.webp",
  price_points: "100",
  stock: 5,
  status: "on_sale",
};

export const order = {
  id: "9007199254741001",
  order_no: "ORD1-abc",
  status: "paid",
  total_points: "200",
  receiver: "张三",
  phone: "13800000000",
  address: "上海市 浦东新区 测试路 1 号",
  items: [
    {
      product_id: "9007199254740993",
      product_name: "测试商品",
      product_image: "https://img.example.test/x.jpg",
      price_snapshot: "100",
      quantity: 2,
    },
  ],
  paid_at: "2026-08-01T00:00:00Z",
  shipped_at: null,
  completed_at: null,
  refunded_at: null,
  created_at: "2026-08-01T00:00:00Z",
};

export const ledgerEntry = {
  id: "9007199254742001",
  type: "admin_adjust",
  delta: "-100",
  balance_after: "900",
  remark: "活动补偿",
  created_at: "2026-08-01T00:00:00Z",
};

export const roles = [
  {
    id: "1",
    name: "super_admin",
    permissions: [
      "product:read",
      "product:write",
      "image:write",
      "order:read",
      "order:ship",
      "refund:read",
      "refund:approve",
      "points:adjust",
      "role:manage",
      "admin:self",
    ],
  },
  {
    id: "2",
    name: "operator",
    permissions: [
      "product:read",
      "product:write",
      "image:write",
      "order:read",
      "order:ship",
      "refund:read",
      "admin:self",
    ],
  },
];