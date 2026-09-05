import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import test from "node:test";
import { parse } from "yaml";

const root = resolve(import.meta.dirname, "../../..");
const document = parse(readFileSync(resolve(root, "docs/api/openapi.yaml"), "utf8"));

const publicInt64Pointers = [
  "#/components/parameters/ProductId/schema",
  "#/components/parameters/CartItemId/schema",
  "#/components/parameters/AddressId/schema",
  "#/components/parameters/OrderId/schema",
  "#/components/parameters/RoleId/schema",
  "#/components/parameters/AdminUserId/schema",
  "#/components/schemas/User/properties/id",
  "#/components/schemas/User/properties/points_balance",
  "#/components/schemas/Product/properties/id",
  "#/components/schemas/Product/properties/price_points",
  "#/components/schemas/ProductWriteRequest/properties/price_points",
  "#/components/schemas/CartItem/properties/id",
  "#/components/schemas/CartItem/properties/product_id",
  "#/components/schemas/Address/properties/id",
  "#/components/schemas/Address/properties/version",
  "#/components/schemas/OrderItem/properties/product_id",
  "#/components/schemas/OrderItem/properties/price_snapshot",
  "#/components/schemas/Order/properties/id",
  "#/components/schemas/Order/properties/total_points",
  "#/components/schemas/LedgerEntry/properties/id",
  "#/components/schemas/LedgerEntry/properties/order_id",
  "#/components/schemas/LedgerEntry/properties/delta",
  "#/components/schemas/LedgerEntry/properties/balance_after",
  "#/components/schemas/Role/properties/id",
  "#/components/schemas/AdminUser/properties/id",
  "#/components/schemas/AdminUserUpdateRequest/properties/role_id",
  "#/components/schemas/AdminLoginResponse/properties/data/properties/admin_id",
  "#/components/schemas/CartItemCreateRequest/properties/product_id",
  "#/components/schemas/OrderCreateRequest/properties/cart_item_ids/items",
  "#/components/schemas/OrderCreateRequest/properties/address_id",
  "#/components/schemas/PointsAdjustRequest/properties/user_id",
  "#/components/schemas/PointsAdjustRequest/properties/delta",
];

function resolvePointer(value, pointer) {
  return pointer
    .slice(2)
    .split("/")
    .map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"))
    .reduce((current, part) => current?.[part], value);
}

test("public int64 markers require string schemas and precise decimal patterns", () => {
  for (const pointer of publicInt64Pointers) {
    const schema = resolvePointer(document, pointer);
    assert.equal(schema?.["x-public-int64"], true, `${pointer} is not marked as public int64`);
    const types = Array.isArray(schema.type) ? schema.type : [schema.type];
    assert.deepEqual(types.filter((type) => type !== "null"), ["string"], `${pointer} must not expose number or integer`);
    assert.equal(schema.format, undefined, `${pointer} must not use an integer format`);
    const validPatterns = schema["x-public-int64-signed"]
      ? new Set(["^-?[0-9]+$", "^-?[1-9][0-9]*$"])
      : new Set(["^[0-9]+$", "^[1-9][0-9]*$"]);
    assert.ok(validPatterns.has(schema.pattern), `${pointer} has the wrong decimal pattern`);
    assert.equal(typeof schema.example, "string", `${pointer} needs a string example`);
  }
});
