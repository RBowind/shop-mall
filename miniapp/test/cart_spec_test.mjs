/**
 * 购物车 spec 用例。这里只放绑定到 `specs/cart/contract.md` 的用例，每条用例上方
 * 写 `// contract: <B 编号>` 回链；模块级单测留在 stores.test.mjs，两边不混。
 */

import assert from "node:assert/strict";
import test from "node:test";

import { checkoutDecision } from "../src/stores/cart.ts";
import { cartItem, offSaleProduct, product } from "./helpers.mjs";

// contract: B9
test("cart checkout is blocked while any line is non-purchasable", () => {
  assert.deepEqual(checkoutDecision([cartItem("11", product, 2)]), { action: "proceed" });

  const blocked = checkoutDecision([
    cartItem("11", product, 2),
    cartItem("12", offSaleProduct, 1),
  ]);
  assert.equal(blocked.action, "blocked");
  assert.equal(blocked.message, "有商品已下架，请先移除");

  assert.equal(checkoutDecision([]).action, "empty");
});