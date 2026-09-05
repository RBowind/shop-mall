import assert from "node:assert/strict";
import test from "node:test";

import { describeApiError, withTraceId } from "../src/lib/errors.ts";
import { ApiRequestError } from "../src/services/request.ts";

function makeError(statusCode, envelope) {
  return new ApiRequestError({ statusCode, data: envelope, headers: {} });
}

const TRACE = "trace-abc-123";

test("404 presents as not_found and keeps the trace ID", () => {
  const presentation = describeApiError(
    makeError(404, { code: 1004, data: null, message: "not found", trace_id: TRACE }),
  );
  assert.equal(presentation.kind, "not_found");
  assert.equal(presentation.retryable, false);
  assert.equal(presentation.traceId, TRACE);
  assert.match(withTraceId(presentation), /trace-abc-123/);
});

test("409 presents as conflict and keeps the trace ID", () => {
  const presentation = describeApiError(
    makeError(409, { code: 2001, data: null, message: "idempotency replay conflict", trace_id: TRACE }),
  );
  assert.equal(presentation.kind, "conflict");
  assert.equal(presentation.retryable, false);
  assert.equal(presentation.traceId, TRACE);
  assert.match(presentation.message, /idempotency replay conflict/);
  assert.match(withTraceId(presentation), /trace-abc-123/);
});

test("422 presents as validation and keeps the trace ID", () => {
  const presentation = describeApiError(
    makeError(422, { code: 2002, data: null, message: "product is off sale", trace_id: TRACE }),
  );
  assert.equal(presentation.kind, "validation");
  assert.equal(presentation.retryable, false);
  assert.equal(presentation.traceId, TRACE);
  assert.match(presentation.message, /product is off sale/);
});

test("5xx presents as server error, keeps the trace ID and is retryable", () => {
  const presentation = describeApiError(
    makeError(500, { code: 5001, data: null, message: "boom", trace_id: TRACE }),
  );
  assert.equal(presentation.kind, "server");
  assert.equal(presentation.retryable, true);
  assert.equal(presentation.traceId, TRACE);
});

test("401 presents as unauthorized (login gate handles it, no retry)", () => {
  const presentation = describeApiError(
    makeError(401, { code: 1002, data: null, message: "unauthorized", trace_id: TRACE }),
  );
  assert.equal(presentation.kind, "unauthorized");
  assert.equal(presentation.retryable, false);
});

test("network errors present as retryable network failures without a trace ID", () => {
  const presentation = describeApiError(new Error("connect ECONNREFUSED"));
  assert.equal(presentation.kind, "network");
  assert.equal(presentation.retryable, true);
  assert.equal(presentation.traceId, undefined);
  assert.equal(withTraceId(presentation), presentation.message);
});

test("trace ID is read from the X-Trace-Id header when the body has none", () => {
  const error = new ApiRequestError({
    statusCode: 422,
    data: { code: 2002, data: null, message: "bad", trace_id: "" },
    headers: { "X-Trace-Id": "header-trace" },
  });
  assert.equal(describeApiError(error).traceId, "header-trace");
});