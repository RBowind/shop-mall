/**
 * Exact non-negative integer decimal arithmetic on int64-as-string values.
 *
 * The contract serializes every int64 field (points, price, stock, quantity)
 * as a string; JS numbers cannot represent large int64 values exactly. BigInt
 * is not guaranteed in every WeChat base library, so display arithmetic uses
 * decimal string math. These helpers are presentational only: the authoritative
 * order total, points balance and refund amount always come from the server,
 * and none of these values is ever sent back in a request.
 */

export function addIntString(a: string, b: string): string {
  const x = a.replace(/^0+/, "") || "0";
  const y = b.replace(/^0+/, "") || "0";
  const left = x.split("").reverse();
  const right = y.split("").reverse();
  const length = Math.max(left.length, right.length);
  const result: number[] = [];
  let carry = 0;
  for (let i = 0; i < length; i += 1) {
    const sum =
      (left[i] ? Number(left[i]) : 0) +
      (right[i] ? Number(right[i]) : 0) +
      carry;
    result.push(sum % 10);
    carry = Math.floor(sum / 10);
  }
  if (carry > 0) result.push(carry);
  return result.reverse().join("");
}

export function mulIntString(value: string, factor: number): string {
  if (factor <= 0) return "0";
  const digits = value.replace(/^0+/, "").split("").reverse();
  const result: number[] = [];
  let carry = 0;
  for (const ch of digits) {
    const product = Number(ch) * factor + carry;
    result.push(product % 10);
    carry = Math.floor(product / 10);
  }
  while (carry > 0) {
    result.push(carry % 10);
    carry = Math.floor(carry / 10);
  }
  return result.reverse().join("") || "0";
}

/** Sum of `price_points * quantity` over lines, as an exact decimal string. */
export function sumLineTotal(
  lines: ReadonlyArray<{ price_points: string; quantity: number }>,
): string {
  return lines.reduce(
    (total, line) =>
      addIntString(total, mulIntString(line.price_points, line.quantity)),
    "0",
  );
}

/** Convenience for cart items: sums server `price_points` over purchasable lines. */
export function sumCartItems(
  items: ReadonlyArray<{ product: { price_points: string }; quantity: number }>,
): string {
  return sumLineTotal(
    items.map((item) => ({
      price_points: item.product.price_points,
      quantity: item.quantity,
    })),
  );
}
/** Compares two non-negative decimal strings; returns -1 / 0 / 1. */
export function compareIntString(a: string, b: string): number {
  const x = a.replace(/^0+/, "") || "0";
  const y = b.replace(/^0+/, "") || "0";
  if (x.length !== y.length) return x.length < y.length ? -1 : 1;
  if (x === y) return 0;
  return x < y ? -1 : 1;
}
