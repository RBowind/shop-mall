import assert from "node:assert/strict";
import test from "node:test";

import { setPreferencesAdapter } from "../src/lib/preferences.ts";
import { configureTransport } from "../src/lib/transport.ts";
import {
  clearCheckoutSession,
  fingerprintOfIntent,
} from "../src/lib/checkout-session.ts";
import { createOrder } from "../src/services/orders.ts";
import { envelope, fakeTransport, memoryAdapter, order } from "./helpers.mjs";

// In-memory persistence so the token survives across calls in this file.
setPreferencesAdapter(memoryAdapter());

const { transport, calls } = fakeTransport(() => envelope(0, order));
configureTransport(transport);

const intentA = {
  lines: [
    { id: "11", quantity: 2 },
    { id: "12", quantity: 1 },
  ],
  addressId: "7",
  addressVersion: "1",
};

function tokenAt(index) {
  return calls[index].headers["Idempotency-Key"];
}

test("same checkout intent reuses the same token and payload", async () => {
  await createOrder(intentA);
  await createOrder(intentA);
  assert.equal(calls.length, 2);
  assert.equal(tokenAt(0), tokenAt(1));
  assert.deepEqual(calls[1].body, calls[0].body);
});

test("a changed quantity mints a new token", async () => {
  await createOrder({
    lines: [
      { id: "11", quantity: 3 },
      { id: "12", quantity: 1 },
    ],
    addressId: "7",
    addressVersion: "1",
  });
  assert.equal(calls.length, 3);
  assert.notEqual(tokenAt(2), tokenAt(1));
});

test("a changed address mints a new token", async () => {
  await createOrder({
    lines: [
      { id: "11", quantity: 3 },
      { id: "12", quantity: 1 },
    ],
    addressId: "8",
    addressVersion: "1",
  });
  assert.equal(calls.length, 4);
  assert.notEqual(tokenAt(3), tokenAt(2));
});

test("an edited address (version bump) alone mints a new token", async () => {
  await createOrder({
    lines: [
      { id: "11", quantity: 3 },
      { id: "12", quantity: 1 },
    ],
    addressId: "8",
    addressVersion: "2",
  });
  assert.equal(calls.length, 5);
  assert.notEqual(tokenAt(4), tokenAt(3));
});

test("a retry after a lost response reuses the persisted token", async () => {
  // Simulates a timeout/lost-response retry: the same intent is submitted again.
  const intent = {
    lines: [
      { id: "11", quantity: 3 },
      { id: "12", quantity: 1 },
    ],
    addressId: "8",
    addressVersion: "2",
  };
  const before = tokenAt(4);
  await createOrder(intent);
  assert.equal(calls.length, 6);
  assert.equal(tokenAt(5), before);
  assert.deepEqual(calls[5].body, calls[4].body);
});

test("clearCheckoutSession drops the token; the next submit mints a fresh one", async () => {
  const before = tokenAt(5);
  clearCheckoutSession();
  await createOrder({
    lines: [
      { id: "11", quantity: 3 },
      { id: "12", quantity: 1 },
    ],
    addressId: "8",
    addressVersion: "2",
  });
  assert.equal(calls.length, 7);
  assert.notEqual(tokenAt(6), before);
});

test("fingerprint is order-independent for the same line set", () => {
  const a = fingerprintOfIntent({
    lines: [
      { id: "12", quantity: 1 },
      { id: "11", quantity: 2 },
    ],
    addressId: "7",
    addressVersion: "1",
  });
  const b = fingerprintOfIntent({
    lines: [
      { id: "11", quantity: 2 },
      { id: "12", quantity: 1 },
    ],
    addressId: "7",
    addressVersion: "1",
  });
  assert.equal(a, b);
});