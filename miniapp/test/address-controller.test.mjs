import assert from "node:assert/strict";
import test from "node:test";

import {
  buildAddressPatch,
  toAddressFormDraft,
  validateAddressForm,
} from "../src/pages/address/controller.ts";
import { address } from "./helpers.mjs";

test("address form validation flags empty receiver and short phone", () => {
  const invalid = validateAddressForm({
    receiver: "   ",
    phone: "138",
    region: "上海市 浦东新区",
    detail: "测试路 1 号",
    isDefault: false,
  });
  assert.ok(invalid.receiver);
  assert.ok(invalid.phone);
  assert.equal(invalid.region, undefined);
  assert.equal(invalid.detail, undefined);

  const valid = validateAddressForm({
    receiver: "张三",
    phone: "13800000000",
    region: "上海市 浦东新区",
    detail: "测试路 1 号",
    isDefault: true,
  });
  assert.deepEqual(valid, {});
});

test("address edit form is prefilled from the current address", () => {
  const draft = toAddressFormDraft(address);
  assert.equal(draft.receiver, "张三");
  assert.equal(draft.phone, "13800000000");
  assert.equal(draft.region, "上海市 浦东新区");
  assert.equal(draft.detail, "测试路 1 号");
  assert.equal(draft.isDefault, true);
});

test("address patch only carries changed fields", () => {
  const changed = buildAddressPatch(address, {
    ...toAddressFormDraft(address),
    detail: "测试路 2 号",
  });
  assert.deepEqual(changed, { detail: "测试路 2 号" });

  const setDefault = buildAddressPatch(
    { ...address, is_default: false },
    { ...toAddressFormDraft({ ...address, is_default: false }), isDefault: true },
  );
  assert.deepEqual(setDefault, { is_default: true });

  const untouched = buildAddressPatch(address, toAddressFormDraft(address));
  assert.deepEqual(untouched, {});

  const cancelled = buildAddressPatch(
    address,
    { ...toAddressFormDraft(address), isDefault: false },
  );
  assert.deepEqual(cancelled, { is_default: false });
});
