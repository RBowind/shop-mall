/**
 * Shared UI feedback helpers. Every failure keeps the server trace ID so the
 * user can report it to support; internal error details are never shown.
 */

import { getTaro } from "./taro.ts";
import { describeApiError, withTraceId } from "./errors.ts";

export function showErrorModal(
  err: unknown,
): void {
  const presentation = describeApiError(err);
  getTaro().showModal({
    title: presentation.title,
    content: withTraceId(presentation),
    showCancel: false,
    confirmText: "知道了",
  });
}

export function showErrorToast(err: unknown): void {
  const presentation = describeApiError(err);
  getTaro().showToast({ title: presentation.title, icon: "none" });
}