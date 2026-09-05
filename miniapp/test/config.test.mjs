import assert from "node:assert/strict";
import test from "node:test";

import { API_BASE_URL } from "../src/config.ts";
import { createAppTransport } from "../src/lib/transport.ts";
import { setTaroForTest } from "../src/lib/taro.ts";

test("API_BASE_URL defaults to the local compose backend origin", () => {
  assert.equal(API_BASE_URL, "http://127.0.0.1:8080");
});

test("app transport prefixes every request with the API base URL", async () => {
  const seen = [];
  setTaroForTest({
    request: async (options) => {
      seen.push(options.url);
      return {
        statusCode: 200,
        data: { code: 0, data: null, message: "ok", trace_id: "t" },
        header: {},
      };
    },
  });

  const transport = createAppTransport(
    "https://api.example.test",
    () => "buyer-token",
    () => {},
  );
  await transport({ method: "GET", path: "/api/v1/products" });
  await transport({ method: "GET", path: "/api/v1/me" });

  assert.equal(seen[0], "https://api.example.test/api/v1/products");
  assert.equal(seen[1], "https://api.example.test/api/v1/me");
});