import assert from "node:assert/strict";
import test from "node:test";

import { createMiniappTransport } from "../src/services/request.ts";

test("miniapp transport keeps public int64 IDs and trace data unchanged", async () => {
  const calls = [];
  const transport = createMiniappTransport({
    baseUrl: "https://api.example.test",
    getAccessToken: () => "buyer-token",
    request: async (options) => {
      calls.push(options);
      return {
        statusCode: 200,
        data: {
          code: 0,
          data: { id: "9007199254740993" },
          message: "ok",
          trace_id: "body-trace",
        },
        header: { "X-Trace-Id": "header-trace" },
      };
    },
  });

  const response = await transport({
    method: "GET",
    path: "/api/v1/products/9007199254740993",
    query: { page: 1 },
  });

  assert.equal(calls.length, 1);
  assert.equal(
    calls[0].url,
    "https://api.example.test/api/v1/products/9007199254740993?page=1",
  );
  assert.equal(calls[0].header.Authorization, "Bearer buyer-token");
  assert.equal(response.data.data.id, "9007199254740993");
  assert.equal(response.data.trace_id, "body-trace");
  assert.equal(response.headers["X-Trace-Id"], "header-trace");
  assert.equal(response.trace_id, "body-trace");
});

test("miniapp transport replaces authorization headers case-insensitively", async () => {
  let receivedHeaders;
  const transport = createMiniappTransport({
    getAccessToken: () => "current-token",
    request: async (options) => {
      receivedHeaders = options.header;
      return {
        statusCode: 200,
        data: { ok: true },
        header: {},
      };
    },
  });

  await transport({
    method: "GET",
    path: "/api/v1/me",
    headers: { authorization: "old-token" },
  });

  const authorizationHeaders = Object.keys(receivedHeaders).filter(
    (name) => name.toLowerCase() === "authorization",
  );
  assert.deepEqual(authorizationHeaders, ["Authorization"]);
  assert.equal(receivedHeaders.Authorization, "Bearer current-token");
});

test("miniapp transport clears the session on 401 without retrying WeChat login", async () => {
  let attempts = 0;
  let clearSessionCalls = 0;
  const transport = createMiniappTransport({
    request: async () => {
      attempts += 1;
      return {
        statusCode: 401,
        data: {
          code: 1001,
          data: null,
          message: "unauthorized",
          trace_id: "login-trace",
        },
        header: { "X-Trace-Id": "login-header-trace" },
      };
    },
    clearSession: () => {
      clearSessionCalls += 1;
    },
  });

  await assert.rejects(
    transport({ method: "POST", path: "/api/v1/auth/wx-login", body: { code: "one-time-code" } }),
    (error) => {
      assert.equal(error.statusCode, 401);
      assert.equal(error.response.data.trace_id, "login-trace");
      assert.equal(error.response.headers["X-Trace-Id"], "login-header-trace");
      return true;
    },
  );

  assert.equal(attempts, 1);
  assert.equal(clearSessionCalls, 1);
});
