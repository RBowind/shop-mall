import assert from "node:assert/strict";
import test from "node:test";

import { toCartItemVM } from "../src/components/cart-item/index.ts";
import { toOrderCardVM, orderStatusText } from "../src/components/order-card/index.ts";
import { toProductCardVM } from "../src/components/product-card/index.ts";
import {
  actionsForOrder,
  canConfirmReceipt,
  canRequestRefund,
} from "../src/pages/order-detail/controller.ts";
import { canSubmitCheckout } from "../src/pages/checkout/controller.ts";
import { buildCheckoutIntent } from "../src/pages/checkout/controller.ts";
import { cartItem, offSaleProduct, order, product } from "./helpers.mjs";

test("product-card view model formats server price and sold-out state", () => {
  const onSale = toProductCardVM(product);
  assert.equal(onSale.priceText, "100 积分");
  assert.equal(onSale.stockText, "剩 5");
  assert.equal(onSale.isSoldOut, false);

  const soldOut = toProductCardVM({ ...product, stock: 0 });
  assert.equal(soldOut.isSoldOut, true);
  assert.equal(soldOut.stockText, "已售罄");

  // 5+ digit prices crowd the narrow meta row: drop the stock line.
  const bigPrice = toProductCardVM({ ...product, price_points: "12000" });
  assert.equal(bigPrice.priceValue, "12000");
  assert.equal(bigPrice.stockText, "");
  assert.equal(bigPrice.isSoldOut, false);
});

test("cart-item view model computes exact string line totals and flags non-purchasable", () => {
  const vm = toCartItemVM(cartItem("11", product, 3));
  assert.equal(vm.lineTotalText, "300 积分");
  assert.equal(vm.nonPurchasable, false);

  const gone = toCartItemVM(cartItem("12", offSaleProduct, 1));
  assert.equal(gone.nonPurchasable, true);
  assert.equal(gone.nonPurchasableReason, "商品已下架");
});

test("order-card maps server order status and totals", () => {
  assert.equal(orderStatusText("paid"), "待发货");
  assert.equal(orderStatusText("refunded"), "已退款");
  const vm = toOrderCardVM(order);
  assert.equal(vm.statusText, "待发货");
  assert.equal(vm.totalText, "200 积分");
  assert.equal(vm.itemCount, 1);
});

test("order-detail actions are driven by the server order status", () => {
  const paid = { ...order, status: "paid" };
  assert.deepEqual(actionsForOrder(paid), ["request_refund"]);
  assert.equal(canRequestRefund(paid), true);
  assert.equal(canConfirmReceipt(paid), false);

  const shipped = { ...order, status: "shipped" };
  assert.deepEqual(actionsForOrder(shipped), ["confirm_receipt"]);
  assert.equal(canRequestRefund(shipped), false);
  assert.equal(canConfirmReceipt(shipped), true);

  const completed = { ...order, status: "completed" };
  assert.deepEqual(actionsForOrder(completed), []);
});

test("checkout controller excludes non-purchasable lines and carries the address version", () => {
  const address = {
    id: "7",
    receiver: "张三",
    phone: "13800000000",
    region: "上海市 浦东新区",
    detail: "测试路 1 号",
    is_default: true,
    version: "3",
  };
  const intent = buildCheckoutIntent(
    [cartItem("11", product, 2), cartItem("12", offSaleProduct, 1), cartItem("13", { ...product, id: "9007199254740995", stock: 0 }, 1)],
    address,
  );
  assert.deepEqual(intent.lines, [{ id: "11", quantity: 2 }]);
  assert.equal(intent.addressId, "7");
  assert.equal(intent.addressVersion, "3");
  assert.equal(canSubmitCheckout([cartItem("11", product, 2)], address), true);
  assert.equal(canSubmitCheckout([cartItem("12", offSaleProduct, 1)], address), false);
  assert.equal(canSubmitCheckout([cartItem("11", product, 2)], null), false);
});
import { compareIntString, mulIntString, sumCartItems } from "../src/lib/points.ts";

test("compareIntString orders decimal strings numerically", () => {
  assert.equal(compareIntString("1000", "2000"), -1);
  assert.equal(compareIntString("2000", "1000"), 1);
  assert.equal(compareIntString("1000", "1000"), 0);
  // Length beats digit order: 999 < 1000.
  assert.equal(compareIntString("999", "1000"), -1);
  // Leading zeros normalize.
  assert.equal(compareIntString("01000", "1000"), 0);
  // Large int64 values compare without Number precision loss.
  assert.equal(compareIntString("9007199254740993", "9007199254740992"), 1);
});
