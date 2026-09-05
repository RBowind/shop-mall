import assert from "node:assert/strict";
import test from "node:test";

import {
  canAdjustPoints,
  canAll,
  canAny,
  hasPermission,
  orderActions,
  productActions,
  refundActions,
} from "../src/access.ts";

test("hasPermission is false for missing or undefined permission sets", () => {
  assert.equal(hasPermission(undefined, "product:write"), false);
  assert.equal(hasPermission(null, "product:write"), false);
  assert.equal(hasPermission(["product:read"], "product:write"), false);
  assert.equal(hasPermission(["product:read"], "product:read"), true);
});

test("canAny and canAll combine permission codes", () => {
  assert.equal(canAny(["product:read"], ["product:write", "product:read"]), true);
  assert.equal(canAny(["product:read"], ["product:write", "image:write"]), false);
  assert.equal(canAll(["product:read", "product:write"], ["product:read", "product:write"]), true);
  assert.equal(canAll(["product:read"], ["product:read", "product:write"]), false);
});

test("a role without product:write sees no upload action (permission-hidden action)", () => {
  const actions = productActions(["product:read", "image:write"]);
  assert.equal(actions.canList, true);
  assert.equal(actions.canWrite, false);
  assert.equal(actions.canUpload, false);
});

test("product:write without image:write keeps the upload hidden", () => {
  const actions = productActions(["product:read", "product:write"]);
  assert.equal(actions.canWrite, true);
  assert.equal(actions.canUpload, false);
});

test("product:write plus image:write unlocks the upload", () => {
  const actions = productActions(["product:read", "product:write", "image:write"]);
  assert.equal(actions.canUpload, true);
});

test("order and refund action sets follow their permission codes", () => {
  assert.equal(orderActions(["order:read"]).canShip, false);
  assert.equal(orderActions(["order:read", "order:ship"]).canShip, true);
  assert.equal(refundActions(["refund:read"]).canApprove, false);
  assert.equal(refundActions(["refund:read", "refund:approve"]).canApprove, true);
  assert.equal(canAdjustPoints(["points:adjust"]), true);
  assert.equal(canAdjustPoints(["product:read"]), false);
});