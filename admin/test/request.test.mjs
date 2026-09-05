import assert from "node:assert/strict";
import test from "node:test";

import { createAdminTransport } from "../src/services/request.ts";

test("admin transport sends cookies, CSRF, JSON strings, and trace metadata", async () => {
  const calls = [];
  const transport = createAdminTransport({
    baseUrl: "https://admin.example.test",
    getCookie: () => "csrf_token=csrf-value; theme=dark",
    fetch: async (url, init) => {
      calls.push({ url, init });
      return new Response(
        JSON.stringify({
          code: 0,
          data: { id: "9007199254740993" },
          message: "ok",
          trace_id: "body-trace",
        }),
        {
          status: 200,
          headers: {
            "content-type": "application/json",
            "X-Trace-Id": "header-trace",
          },
        },
      );
    },
  });

  const response = await transport({
    method: "POST",
    path: "/api/admin/v1/products",
    headers: { "Idempotency-Key": "request-1" },
    body: { price_points: "9007199254740993" },
  });

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "https://admin.example.test/api/admin/v1/products");
  assert.equal(calls[0].init.credentials, "include");
  assert.equal(calls[0].init.headers["X-CSRF-Token"], "csrf-value");
  assert.equal(calls[0].init.headers["Idempotency-Key"], "request-1");
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    price_points: "9007199254740993",
  });
  assert.equal(response.data.data.id, "9007199254740993");
  assert.equal(response.data.trace_id, "body-trace");
  assert.equal(response.headers["X-Trace-Id"], "header-trace");
  assert.equal(response.trace_id, "body-trace");
});

test("admin login does not add a CSRF header, while 401 redirects without retrying", async () => {
  let attempts = 0;
  let redirects = 0;
  const transport = createAdminTransport({
    getCookie: () => "csrf_token=csrf-value",
    redirectToLogin: () => {
      redirects += 1;
    },
    fetch: async (_url, init) => {
      attempts += 1;
      if (init.headers["X-CSRF-Token"] !== undefined) {
        throw new Error("login must not require a CSRF header");
      }
      return new Response(
        JSON.stringify({
          code: 1001,
          data: null,
          message: "unauthorized",
          trace_id: "login-trace",
        }),
        {
          status: 401,
          headers: {
            "content-type": "application/json",
            "X-Trace-Id": "login-header-trace",
          },
        },
      );
    },
  });

  await assert.rejects(
    transport({
      method: "POST",
      path: "/api/admin/v1/auth/login",
      body: { username: "admin", password: "password" },
    }),
    (error) => {
      assert.equal(error.statusCode, 401);
      assert.equal(error.response.data.trace_id, "login-trace");
      assert.equal(error.response.headers["X-Trace-Id"], "login-header-trace");
      return true;
    },
  );

  assert.equal(attempts, 1);
  assert.equal(redirects, 1);
});

test("admin 401 redirects before handling an invalid JSON body", async () => {
  let redirects = 0;
  const transport = createAdminTransport({
    redirectToLogin: () => {
      redirects += 1;
    },
    fetch: async () => ({
      status: 401,
      headers: new Headers({
        "content-type": "application/json",
        "X-Trace-Id": "invalid-json-trace",
      }),
      text: async () => "{invalid-json",
    }),
  });

  await assert.rejects(
    transport({ method: "GET", path: "/api/admin/v1/products" }),
    (error) => {
      assert.equal(error.statusCode, 401);
      assert.equal(error.response.statusCode, 401);
      assert.equal(error.response.headers["X-Trace-Id"], "invalid-json-trace");
      return true;
    },
  );

  assert.equal(redirects, 1);
});

test("admin writes cannot bypass cookie-bound CSRF or browser Origin handling", async () => {
  let receivedHeaders;
  let receivedInit;
  const transport = createAdminTransport({
    getCookie: () => "",
    fetch: async (_url, init) => {
      receivedHeaders = init.headers;
      receivedInit = init;
      return new Response("", { status: 200 });
    },
  });

  await transport({
    method: "POST",
    path: "/api/admin/v1/products",
    headers: {
      "X-CSRF-Token": "forged-token",
      Origin: "https://forged.example.test",
    },
    body: { name: "product" },
  });

  assert.equal(receivedHeaders["X-CSRF-Token"], undefined);
  assert.equal(receivedHeaders.Origin, undefined);
  assert.equal(receivedInit.mode, "cors");
});
