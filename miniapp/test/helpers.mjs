/**
 * Shared test helpers: in-memory preferences adapter and a recording fake
 * transport that mimics the real `createMiniappTransport` behavior: it wraps a
 * plain envelope in a 200 response and throws `ApiRequestError` for every
 * non-2xx status.
 */

import { ApiRequestError } from "../src/services/request.ts";

export function memoryAdapter() {
  const map = new Map();
  return {
    getItem(key) {
      return map.has(key) ? map.get(key) : null;
    },
    setItem(key, value) {
      map.set(key, value);
    },
    removeItem(key) {
      map.delete(key);
    },
  };
}

export function fakeTransport(respond, calls = []) {
  const transport = async (input) => {
    calls.push(input);
    const result = await respond(input, calls);
    const response =
      result !== null && typeof result === "object" && "statusCode" in result
        ? result
        : { statusCode: 200, data: result, header: {} };
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

export const product = {
  id: "9007199254740993",
  name: "测试商品",
  description: "desc",
  main_image: "https://img.example.test/x.jpg",
  price_points: "100",
  stock: 5,
  status: "on_sale",
};

export const user = {
  id: "9007199254740993",
  nickname: "买家",
  avatar_url: "https://img.example.test/avatar.jpg",
  points_balance: "1000",
};

export const loginEnvelope = () =>
  envelope(0, { access_token: "jwt-token", user });

export const offSaleProduct = {
  ...product,
  id: "9007199254740994",
  name: "已下架商品",
  status: "off_sale",
  stock: 0,
};

export const cartItem = (id, prod, quantity = 1) => ({
  id,
  product_id: prod.id,
  quantity,
  product: prod,
});

export const address = {
  id: "7",
  receiver: "张三",
  phone: "13800000000",
  region: "上海市 浦东新区",
  detail: "测试路 1 号",
  is_default: true,
  version: "1",
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