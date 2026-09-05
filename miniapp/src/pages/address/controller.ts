/**
 * Address page logic: form validation, prefill and patch construction.
 *
 * The patch is the client-side half of spec B8: `PATCH /api/v1/addresses`
 * carries only changed fields, aligned with the backend trim semantics
 * (values are trimmed before comparing and sending).
 */

import type {
  Address,
  AddressUpdateRequest,
} from "../../services/types.ts";

export type AddressFormDraft = {
  receiver: string;
  phone: string;
  region: string;
  detail: string;
  isDefault: boolean;
};

export type AddressFormErrors = {
  receiver?: string;
  phone?: string;
  region?: string;
  detail?: string;
};

export function validateAddressForm(draft: AddressFormDraft): AddressFormErrors {
  const errors: AddressFormErrors = {};
  if (draft.receiver.trim() === "" || draft.receiver.trim().length > 32) {
    errors.receiver = "收件人需为 1-32 个字";
  }
  const phone = draft.phone.trim();
  if (phone.length < 6 || phone.length > 20) {
    errors.phone = "手机号需为 6-20 个字";
  }
  if (draft.region.trim() === "" || draft.region.trim().length > 64) {
    errors.region = "所在地区需为 1-64 个字";
  }
  if (draft.detail.trim() === "" || draft.detail.trim().length > 255) {
    errors.detail = "详细地址需为 1-255 个字";
  }
  return errors;
}

export function toAddressFormDraft(addr: Address): AddressFormDraft {
  return {
    receiver: addr.receiver,
    phone: addr.phone,
    region: addr.region,
    detail: addr.detail,
    isDefault: addr.is_default,
  };
}

export function buildAddressPatch(
  original: Address,
  draft: AddressFormDraft,
): AddressUpdateRequest {
  const patch: AddressUpdateRequest = {};
  const receiver = draft.receiver.trim();
  if (receiver !== original.receiver) {
    patch.receiver = receiver;
  }
  const phone = draft.phone.trim();
  if (phone !== original.phone) {
    patch.phone = phone;
  }
  const region = draft.region.trim();
  if (region !== original.region) {
    patch.region = region;
  }
  const detail = draft.detail.trim();
  if (detail !== original.detail) {
    patch.detail = detail;
  }
  if (draft.isDefault !== original.is_default) {
    patch.is_default = draft.isDefault;
  }
  return patch;
}
