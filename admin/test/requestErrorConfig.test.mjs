import assert from "node:assert/strict";
import test from "node:test";

import { ApiRequestError } from "../src/services/request.ts";
import {
  describeAdminError,
  isApiRequestError,
  traceOfEnvelope,
  withTraceId,
} from "../src/requestErrorConfig.ts";

function error(statusCode, data, headers = {}) {
  return new ApiRequestError({ statusCode, data, headers });
}

test("401 maps to an unauthorized presentation that redirects to login", () => {
  const presentation = describeAdminError(
    error(401, { code: 1001, data: null, message: "unauthorized", trace_id: "t-401" }),
  );
  assert.equal(presentation.kind, "unauthorized");
  assert.equal(presentation.redirectToLogin, true);
  assert.equal(presentation.traceId, "t-401");
});

test("403 maps to a permission-denied display", () => {
  const presentation = describeAdminError(
    error(403, { code: 1002, data: null, message: "forbidden", trace_id: "t-403" }),
  );
  assert.equal(presentation.kind, "forbidden");
  assert.equal(presentation.title, "没有权限");
  assert.equal(presentation.message, "forbidden");
  assert.equal(presentation.redirectToLogin, false);
});

test("403 without a message falls back to the permission-denied hint", () => {
  const presentation = describeAdminError(error(403, { code: 1002, data: null, message: "", trace_id: "t" }));
  assert.match(presentation.message, /没有执行此操作/);
});

test("409 and 422 surface the business message with the trace id", () => {
  const conflict = describeAdminError(
    error(409, { code: 2001, data: null, message: "状态已变化", trace_id: "t-409" }),
  );
  assert.equal(conflict.kind, "conflict");
  assert.equal(conflict.traceId, "t-409");

  const validation = describeAdminError(
    error(422, { code: 2002, data: null, message: "备注不能为空", trace_id: "t-422" }),
  );
  assert.equal(validation.kind, "validation");
  assert.equal(validation.message, "备注不能为空");
  assert.equal(validation.traceId, "t-422");
});

test("5xx maps to a server error and carries the trace id", () => {
  const presentation = describeAdminError(
    error(500, { code: 5001, data: null, message: "boom", trace_id: "t-500" }),
  );
  assert.equal(presentation.kind, "server");
  assert.equal(presentation.retryable, true);
  assert.equal(presentation.traceId, "t-500");
});

test("the trace id falls back to the X-Trace-Id header when the body has none", () => {
  const presentation = describeAdminError(
    error(503, { code: 5001, data: null, message: "down", trace_id: "" }, { "x-trace-id": "hdr-trace" }),
  );
  assert.equal(presentation.kind, "server");
  assert.equal(presentation.traceId, "hdr-trace");
  assert.match(withTraceId(presentation), /trace: hdr-trace/);
});

test("trace_id falls back from response to header to envelope", () => {
  assert.equal(traceOfEnvelope({ trace_id: "body" }), "body");
  assert.equal(traceOfEnvelope({ trace_id: "" }), undefined);
  assert.equal(traceOfEnvelope(null), undefined);
});

test("withTraceId appends the trace id when present", () => {
  const presentation = describeAdminError(
    error(422, { code: 2002, data: null, message: "备注不能为空", trace_id: "t-422" }),
  );
  assert.match(withTraceId(presentation), /备注不能为空.*trace: t-422/);
});

test("network failures map to a retryable network presentation", () => {
  const presentation = describeAdminError(new TypeError("fetch failed"));
  assert.equal(presentation.kind, "network");
  assert.equal(presentation.retryable, true);
  assert.equal(presentation.traceId, undefined);
  assert.equal(isApiRequestError(new Error("x")), false);
});