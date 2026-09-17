/**
 * Buyer cart store.
 *
 * Holds the server-returned cart items (quantity, product price, stock and
 * status are all server-owned). A cart item whose product went off sale or ran
 * out of stock stays visible but is flagged non-purchasable; the cart and
 * checkout pages never submit it.
 */

import { createStore } from "zustand/vanilla";

import { describeApiError } from "../lib/errors.ts";
import {
  addCartItem,
  deleteCartItem,
  getCart,
  updateCartItem,
} from "../services/cart.ts";
import type { CartItem } from "../services/types.ts";

export interface CartError {
  message: string;
  traceId?: string;
}

export interface CartState {
  items: CartItem[];
  loading: boolean;
  error: CartError | null;
}

const store = createStore<CartState>(() => ({
  items: [],
  loading: false,
  error: null,
}));

export interface CartStore {
  getState(): CartState;
  subscribe(listener: () => void): () => void;
  refresh(): Promise<void>;
  add(productId: string, quantity: number): Promise<void>;
  update(itemId: string, quantity: number): Promise<void>;
  remove(itemId: string): Promise<void>;
}

export const cartStore: CartStore = {
  getState: store.getState,
  subscribe: store.subscribe,

  async refresh() {
    store.setState({ loading: true, error: null });
    try {
      const items = await getCart();
      store.setState({ items, loading: false });
    } catch (err) {
      const presentation = describeApiError(err);
      store.setState({
        loading: false,
        error: {
          message: presentation.message,
          traceId: presentation.traceId,
        },
      });
      throw err;
    }
  },

  async add(productId, quantity) {
    await addCartItem(productId, quantity);
    await cartStore.refresh();
  },

  async update(itemId, quantity) {
    await updateCartItem(itemId, quantity);
    await cartStore.refresh();
  },

  async remove(itemId) {
    await deleteCartItem(itemId);
    await cartStore.refresh();
  },
};

/** A cart item is non-purchasable when its product is off sale or out of stock. */
export function isNonPurchasable(item: CartItem): boolean {
  return item.product.status === "off_sale" || item.product.stock <= 0;
}

export function purchasableItems(items: CartItem[]): CartItem[] {
  return items.filter((item) => !isNonPurchasable(item));
}

export interface CheckoutDecision {
  action: "proceed" | "blocked" | "empty";
  message?: string;
}

/** 结算入口的判定：购物车的 全选 是静态的，没有「只结算勾选行」的交互，所以只要有一行不可购就整体拦下。 */
export function checkoutDecision(items: CartItem[]): CheckoutDecision {
  if (items.some(isNonPurchasable)) {
    return { action: "blocked", message: "有商品已下架，请先移除" };
  }
  if (items.length === 0) {
    return { action: "empty", message: "购物车是空的" };
  }
  return { action: "proceed" };
}