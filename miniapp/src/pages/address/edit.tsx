/**
 * Address create/edit page (subpackage). Requires login. One page serves both
 * modes: with `addressId` in the route it edits (prefilled from the address
 * list), without it creates a new address.
 *
 * Create sends `is_default` only when the switch is on (the field is optional
 * in AddressCreateRequest, and unchecked means the key is absent). Edit sends
 * a patch of changed fields only via buildAddressPatch. Validation failures
 * block the request and keep the form; server 400 keeps the form too; a 404
 * on edit returns to the address list after the user confirms.
 */

import { Text, View } from "@tarojs/components";
import { useLoad } from "@tarojs/taro";
import { useEffect, useState } from "react";

import { describeApiError, withTraceId } from "../../lib/errors";
import { getTaro } from "../../lib/taro";
import { showErrorModal } from "../../lib/ui";
import {
  createAddress,
  listAddresses,
  updateAddress,
} from "../../services/addresses";
import type {
  Address,
  AddressCreateRequest,
} from "../../services/types";
import { authStore } from "../../stores/auth";
import "./edit.css";
import {
  buildAddressPatch,
  toAddressFormDraft,
  validateAddressForm,
  type AddressFormDraft,
  type AddressFormErrors,
} from "./controller";

const EMPTY_DRAFT: AddressFormDraft = {
  receiver: "",
  phone: "",
  region: "",
  detail: "",
  isDefault: false,
};

type EditableField = "receiver" | "phone" | "region" | "detail";

export function AddressEditPage() {
  const [addressId, setAddressId] = useState("");
  const [original, setOriginal] = useState<Address | null>(null);
  const [draft, setDraft] = useState<AddressFormDraft>(EMPTY_DRAFT);
  const [errors, setErrors] = useState<AddressFormErrors>({});
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [errorText, setErrorText] = useState("");

  const isEdit = addressId !== "";

  useLoad<{ addressId?: string }>((options) => {
    setAddressId(options.addressId ?? "");
  });

  useEffect(() => {
    if (!addressId) return;
    if (!authStore.requireLogin()) return;
    setLoading(true);
    setErrorText("");
    listAddresses()
      .then((list) => {
        const found = list.find((item) => item.id === addressId);
        setLoading(false);
        if (!found) {
          setErrorText("该地址不存在或已被删除");
          return;
        }
        setOriginal(found);
        setDraft(toAddressFormDraft(found));
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setLoading(false);
        setErrorText(withTraceId(presentation));
      });
  }, [addressId]);

  const updateField = (field: EditableField, value: string) => {
    setDraft((current) => ({ ...current, [field]: value }));
    setErrors((current) => ({ ...current, [field]: undefined }));
  };

  const updateDefault = (checked: boolean) => {
    setDraft((current) => ({ ...current, isDefault: checked }));
  };

  const handleSubmitError = (err: unknown) => {
    setSubmitting(false);
    const presentation = describeApiError(err);
    if (isEdit && presentation.kind === "not_found") {
      getTaro().showModal({
        title: presentation.title,
        content: withTraceId(presentation),
        showCancel: false,
        confirmText: "知道了",
        success: () => getTaro().navigateBack(),
      });
      return;
    }
    showErrorModal(err);
  };

  const handleSubmit = () => {
    if (submitting) return;
    const fieldErrors = validateAddressForm(draft);
    const hasErrors = Object.values(fieldErrors).some(Boolean);
    if (hasErrors) {
      setErrors(fieldErrors);
      return;
    }
    setSubmitting(true);
    if (isEdit && original) {
      const patch = buildAddressPatch(original, draft);
      if (Object.keys(patch).length === 0) {
        setSubmitting(false);
        getTaro().navigateBack();
        return;
      }
      updateAddress(addressId, patch)
        .then(() => {
          setSubmitting(false);
          getTaro().showToast({ title: "已保存", icon: "success" });
          getTaro().navigateBack();
        })
        .catch(handleSubmitError);
    } else {
      const payload: AddressCreateRequest = {
        receiver: draft.receiver.trim(),
        phone: draft.phone.trim(),
        region: draft.region.trim(),
        detail: draft.detail.trim(),
      };
      if (draft.isDefault) {
        payload.is_default = true;
      }
      createAddress(payload)
        .then(() => {
          setSubmitting(false);
          getTaro().showToast({ title: "已保存", icon: "success" });
          getTaro().navigateBack();
        })
        .catch(handleSubmitError);
    }
  };

  return (
    <View className="page">
      {loading ? (
        <View className="loading-block">
          <t-loading theme="circular" size="48rpx" text="加载中…" />
        </View>
      ) : null}
      {errorText ? (
        <View className="address-error">
          <Text className="error">{errorText}</Text>
          <t-button
            size="small"
            variant="outline"
            onTap={() => getTaro().navigateBack()}
          >
            返回
          </t-button>
        </View>
      ) : null}
      {!loading && !errorText ? (
        <View className="address-edit">
          <View className="address-form-card">
            <t-cell-group bordered={false}>
              <t-input
                value={draft.receiver}
                label="收件人"
                placeholder="收件人姓名"
                clearable
                status={errors.receiver ? "error" : undefined}
                tips={errors.receiver}
                onChange={(e: { detail: { value: string } }) => updateField("receiver", e.detail.value)}
              />
              <t-input
                value={draft.phone}
                label="手机号"
                placeholder="联系电话"
                clearable
                status={errors.phone ? "error" : undefined}
                tips={errors.phone}
                onChange={(e: { detail: { value: string } }) => updateField("phone", e.detail.value)}
              />
            </t-cell-group>
          </View>
          <View className="address-form-card">
            <t-cell-group bordered={false}>
              <t-input
                value={draft.region}
                label="所在地区"
                placeholder="省市区"
                clearable
                status={errors.region ? "error" : undefined}
                tips={errors.region}
                onChange={(e: { detail: { value: string } }) => updateField("region", e.detail.value)}
              />
              <t-textarea
                value={draft.detail}
                label="详细地址"
                placeholder="街道、门牌号等"
                autosize
                onChange={(e: { detail: { value: string } }) => updateField("detail", e.detail.value)}
              />
              {errors.detail ? (
                <Text className="text-error address-form-card__detail-error">
                  {errors.detail}
                </Text>
              ) : null}
            </t-cell-group>
          </View>
          <View className="address-form-card address-default-row">
            <Text className="address-default-row__label">设为默认地址</Text>
            <t-switch
              value={draft.isDefault}
              onChange={(e: { detail: { value: boolean } }) => updateDefault(e.detail.value)}
            />
          </View>
          <View className="address-form-submit">
            <t-button
              theme="primary"
              block
              loading={submitting}
              onTap={handleSubmit}
            >
              保存
            </t-button>
          </View>
        </View>
      ) : null}
    </View>
  );
}

export default AddressEditPage;
