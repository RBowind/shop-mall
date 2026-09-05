/**
 * User-facing error presentation for the miniapp.
 *
 * Every failed request keeps the server `trace_id` (from the response body or
 * the `X-Trace-Id` header) so the user can report it to support. The mapping
 * covers the buyer-facing contract: 404, 409, 422, 429, 5xx and network
 * failures each get a distinct presentation. 401 is special: presentation is
 * handled by the login gate, and 5xx/network errors are the only retryable
 * kinds.
 */

import { ApiRequestError } from "../services/request.ts";

export type ApiErrorKind =
  | "unauthorized"
  | "not_found"
  | "conflict"
  | "validation"
  | "rate_limited"
  | "server"
  | "network"
  | "unknown";

export interface ApiErrorPresentation {
  kind: ApiErrorKind;
  title: string;
  message: string;
  traceId: string | undefined;
  retryable: boolean;
}

interface ErrorEnvelope {
  code?: number;
  data?: unknown;
  message?: string;
  trace_id?: string;
}

export function isApiRequestError(err: unknown): err is ApiRequestError {
  return err instanceof ApiRequestError;
}

function findHeaderValue(
  headers: Record<string, string> | undefined,
  name: string,
): string | undefined {
  if (!headers) return undefined;
  const wanted = name.toLowerCase();
  const entry = Object.entries(headers).find(
    ([key, value]) => key.toLowerCase() === wanted && value !== "",
  );
  return entry?.[1];
}

export function traceOfError(err: ApiRequestError): string | undefined {
  return (
    err.response.trace_id ??
    findHeaderValue(err.response.headers, "x-trace-id") ??
    traceOfEnvelope(err.response.data)
  );
}

export function traceOfEnvelope(data: unknown): string | undefined {
  if (data !== null && typeof data === "object") {
    const traceId = (data as ErrorEnvelope).trace_id;
    if (typeof traceId === "string" && traceId !== "") return traceId;
  }
  return undefined;
}

function messageOf(err: ApiRequestError): string {
  const envelope = err.response.data as ErrorEnvelope | undefined;
  const message = envelope?.message;
  return typeof message === "string" && message !== "" ? message : "";
}

export function describeApiError(err: unknown): ApiErrorPresentation {
  if (isApiRequestError(err)) {
    const status = err.statusCode;
    const traceId = traceOfError(err);
    if (status === 401) {
      return {
        kind: "unauthorized",
        title: "登录已失效",
        message: "请重新登录后再试",
        traceId,
        retryable: false,
      };
    }
    if (status === 404) {
      return {
        kind: "not_found",
        title: "内容不存在或已下架",
        message: messageOf(err) || "该内容不存在或已下架",
        traceId,
        retryable: false,
      };
    }
    if (status === 409) {
      return {
        kind: "conflict",
        title: "状态已变化",
        message: messageOf(err) || "请求状态已变化，请刷新后重新确认",
        traceId,
        retryable: false,
      };
    }
    if (status === 422) {
      return {
        kind: "validation",
        title: "无法完成操作",
        message: messageOf(err) || "请求未通过校验，请检查后重试",
        traceId,
        retryable: false,
      };
    }
    if (status === 429) {
      return {
        kind: "rate_limited",
        title: "操作过于频繁",
        message: messageOf(err) || "请稍后再试",
        traceId,
        retryable: true,
      };
    }
    if (status >= 500) {
      return {
        kind: "server",
        title: "服务暂时不可用",
        message: "服务开小差了，请稍后重试",
        traceId,
        retryable: true,
      };
    }
    return {
      kind: "unknown",
      title: "请求失败",
      message: messageOf(err) || "请求未成功，请稍后重试",
      traceId,
      retryable: false,
    };
  }
  return {
    kind: "network",
    title: "网络异常",
    message: "网络连接不稳定，请检查网络后重试",
    traceId: undefined,
    retryable: true,
  };
}

/** Appends the trace ID to a message when one is available. */
export function withTraceId(
  presentation: ApiErrorPresentation,
): string {
  return presentation.traceId
    ? `${presentation.message}（trace: ${presentation.traceId}）`
    : presentation.message;
}