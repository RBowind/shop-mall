/**
 * Address management page (subpackage). Requires login. Lists the buyer's
 * addresses with set-default and delete actions; create and edit live on the
 * sibling edit page.
 *
 * Failure handling follows the spec: set-default and delete failures other
 * than 404 leave the list untouched; a 404 means the entry is stale, so the
 * list is refreshed and the stale entry disappears. Set-default re-fetches on
 * success too, because the server moves the previous default off its flag.
 */

import { Text, View } from "@tarojs/components";
import { useDidShow } from "@tarojs/taro";
import { useState } from "react";

import { describeApiError } from "../../lib/errors";
import { getTaro } from "../../lib/taro";
import { showErrorToast } from "../../lib/ui";
import {
  deleteAddress,
  listAddresses,
  updateAddress,
} from "../../services/addresses";
import type { Address } from "../../services/types";
import { authStore } from "../../stores/auth";
import "./index.css";

export function AddressListPage() {
  const [addresses, setAddresses] = useState<Address[]>([]);
  const [loading, setLoading] = useState(false);
  const [errorText, setErrorText] = useState("");

  const loadAddresses = () => {
    setLoading(true);
    setErrorText("");
    listAddresses()
      .then((list) => {
        setAddresses(list);
        setLoading(false);
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setLoading(false);
        setErrorText(
          presentation.traceId
            ? `${presentation.message}（trace: ${presentation.traceId}）`
            : presentation.message,
        );
      });
  };

  useDidShow(() => {
    if (!authStore.requireLogin()) return;
    loadAddresses();
  });

  const goEdit = (addr?: Address) => {
    const url = addr
      ? `/pages/address/edit?addressId=${encodeURIComponent(addr.id)}`
      : "/pages/address/edit";
    getTaro().navigateTo({ url });
  };

  const handleSetDefault = (addr: Address) => {
    updateAddress(addr.id, { is_default: true })
      .then(() => loadAddresses())
      .catch((err: unknown) => {
        showErrorToast(err);
        if (describeApiError(err).kind === "not_found") {
          loadAddresses();
        }
      });
  };

  const handleDelete = (addr: Address) => {
    getTaro().showModal({
      title: "删除地址",
      content: `确定删除 ${addr.receiver} 的收货地址吗？`,
      confirmText: "删除",
      success: (res) => {
        if (!res.confirm) return;
        deleteAddress(addr.id)
          .then(() => {
            setAddresses((current) =>
              current.filter((item) => item.id !== addr.id),
            );
          })
          .catch((err: unknown) => {
            showErrorToast(err);
            if (describeApiError(err).kind === "not_found") {
              loadAddresses();
            }
          });
      },
    });
  };

  return (
    <View className="page page--with-footer address-page">
      {errorText ? (
        <View className="address-error">
          <Text className="error">{errorText}</Text>
          <t-button
            size="small"
            variant="outline"
            theme="primary"
            onTap={() => loadAddresses()}
          >
            重试
          </t-button>
        </View>
      ) : null}
      {loading && addresses.length === 0 ? (
        <View className="loading-block">
          <t-loading theme="circular" size="48rpx" text="加载中…" />
        </View>
      ) : null}
      {addresses.map((addr) => (
        <View className="address-card" key={addr.id} onClick={() => goEdit(addr)}>
          <View className="address-card__head">
            {/* Default marker doubles as the set-default control: filled blue
                check = current default; empty circle = tap to set (same call
                as the old 设为默认 button). */}
            <View
              className={
                addr.is_default
                  ? "address-card__check address-card__check--on"
                  : "address-card__check"
              }
              aria-label={addr.is_default ? "默认地址" : "设为默认"}
              onClick={(e: { stopPropagation(): void }) => {
                e.stopPropagation();
                if (!addr.is_default) handleSetDefault(addr);
              }}
            >
              {addr.is_default ? (
                <t-icon name="check" size="30rpx" color="#ffffff" />
              ) : null}
            </View>
            <Text className="address-card__receiver">{addr.receiver}</Text>
            <Text className="address-card__phone">{addr.phone}</Text>
            {addr.is_default ? (
              <Text className="address-card__pill">默认</Text>
            ) : null}
          </View>
          <Text className="address-card__detail">
            {addr.region} {addr.detail}
          </Text>
          <View className="address-card__actions">
            <Text
              className="address-card__action"
              onClick={(e: { stopPropagation(): void }) => {
                e.stopPropagation();
                goEdit(addr);
              }}
            >
              编辑
            </Text>
            <Text
              className="address-card__action address-card__action--danger"
              onClick={(e: { stopPropagation(): void }) => {
                e.stopPropagation();
                handleDelete(addr);
              }}
            >
              删除
            </Text>
          </View>
        </View>
      ))}
      {!loading && addresses.length === 0 && !errorText ? (
        <View className="empty-block">
          <t-empty description="还没有收货地址" />
        </View>
      ) : null}
      <View className="address-footer">
        <t-button theme="primary" block onTap={() => goEdit()}>
          新增收货地址
        </t-button>
      </View>
    </View>
  );
}

export default AddressListPage;
