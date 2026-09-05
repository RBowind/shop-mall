/**
 * Checkout intent construction and submission decisions.
 *
 * The intent fingerprint (cart lines + quantities + address id/version) is
 * the client-side trigger for minting a new `Idempotency-Key`: unchanged
 * intent reuses the persisted token so a timeout or lost response replays the
 * original order; only changed lines, quantities or address yield a new token.
 */

import type { CheckoutIntent } from "../../lib/checkout-session.ts";
import type { Address, CartItem } from "../../services/types.ts";
import { purchasableItems } from "../../stores/cart.ts";

export function buildCheckoutIntent(
  cartItems: CartItem[],
  address: Address,
): CheckoutIntent {
  return {
    lines: purchasableItems(cartItems).map((item) => ({
      id: item.id,
      quantity: item.quantity,
    })),
    addressId: address.id,
    addressVersion: address.version,
  };
}

export function canSubmitCheckout(
  cartItems: CartItem[],
  address: Address | null,
): boolean {
  return address !== null && purchasableItems(cartItems).length > 0;
}