/**
 * Unified admin error handling.
 *
 * Maps every transport failure to a user-facing presentation carrying the
 * server trace ID. The 401 redirect itself is performed by the transport
 * (`createAdminTransport` calls `redirectToLogin` before it throws); this
 * module only classifies 401 so the app layer can decide whether to act again,
 * and every other status gets a dedicated display:
 *
 * - 401  -> session expired, route to login
 * - 403  -> permission denied (also covers CSRF/Origin rejection)
 * - 409  -> state transition conflict
 * - 422  -> business rule violation
 * - 429  -> rate limited
 * - 5xx  -> server error
 * - else -> unknown request failure
 * - non-ApiRequestError -> network failure
 *
 * The frontend never acts as the security boundary: a 403 shown here is the
 * backend's decision, not a client-side guess.
 */

import { ApiRequestError } from "./services/request.ts";

export type AdminErrorKind =
  | "unauthorized"
  | "forbidden"
  | "conflict"
  | "validation"
  | "rate_limited"
  | "server"
  | "network"
  | "unknown";

export interface AdminErrorPresentation {
  kind: AdminErrorKind;
  title: string;
  message: string;
  traceId: string | undefined;
  retryable: boolean;
  /** True for 401: the session is gone and the caller should send the user to the login page. */
  redirectToLogin: boolean;
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

export function traceOfEnvelope(data: unknown): string | undefined {
  if (data !== null && typeof data === "object") {
    const traceId = (data as ErrorEnvelope).trace_id;
    if (typeof traceId === "string" && traceId !== "") return traceId;
  }
  return undefined;
}

export function traceOfError(err: ApiRequestError): string | undefined {
  return (
    err.response.trace_id ??
    findHeaderValue(err.response.headers, "x-trace-id") ??
    traceOfEnvelope(err.response.data)
  );
}

function messageOf(err: ApiRequestError): string {
  const envelope = err.response.data as ErrorEnvelope | undefined;
  const message = envelope?.message;
  return typeof message === "string" && message !== "" ? message : "";
}

export function describeAdminError(err: unknown): AdminErrorPresentation {
  if (isApiRequestError(err)) {
    const status = err.statusCode;
    const traceId = traceOfError(err);
    if (status === 401) {
      return {
        kind: "unauthorized",
        title: "登录已失效",
        message: "登录状态已失效，请重新登录后再试",
        traceId,
        retryable: false,
        redirectToLogin: true,
      };
    }
    if (status === 403) {
      return {
        kind: "forbidden",
        title: "没有权限",
        message: messageOf(err) || "当前账号没有执行此操作的权限，请联系管理员",
        traceId,
        retryable: false,
        redirectToLogin: false,
      };
    }
    if (status === 409) {
      return {
        kind: "conflict",
        title: "状态已变化",
        message: messageOf(err) || "请求的数据状态已变化，请刷新后重新确认",
        traceId,
        retryable: false,
        redirectToLogin: false,
      };
    }
    if (status === 422) {
      return {
        kind: "validation",
        title: "无法完成操作",
        message: messageOf(err) || "请求未通过业务校验，请检查输入后重试",
        traceId,
        retryable: false,
        redirectToLogin: false,
      };
    }
    if (status === 429) {
      return {
        kind: "rate_limited",
        title: "操作过于频繁",
        message: messageOf(err) || "操作太频繁，请稍后再试",
        traceId,
        retryable: true,
        redirectToLogin: false,
      };
    }
    if (status >= 500) {
      return {
        kind: "server",
        title: "服务暂时不可用",
        message: "服务暂时不可用，请稍后重试",
        traceId,
        retryable: true,
        redirectToLogin: false,
      };
    }
    return {
      kind: "unknown",
      title: "请求失败",
      message: messageOf(err) || "请求未成功，请稍后重试",
      traceId,
      retryable: false,
      redirectToLogin: false,
    };
  }
  return {
    kind: "network",
    title: "网络异常",
    message: "无法连接服务器，请检查网络后重试",
    traceId: undefined,
    retryable: true,
    redirectToLogin: false,
  };
}

/** Appends the trace ID to a message when one is available. */
export function withTraceId(presentation: AdminErrorPresentation): string {
  return presentation.traceId
    ? `${presentation.message}（trace: ${presentation.traceId}）`
    : presentation.message;
}