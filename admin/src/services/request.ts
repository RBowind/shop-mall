import type { ApiRequest, ApiResponse, ApiTransport } from "./generated/api";

export interface FetchRequestInit {
  method: string;
  headers: Record<string, string>;
  body?: unknown;
  credentials: "include";
  // Browser CORS generates Origin; the server still validates its allowlist.
  mode: "cors";
}

export interface FetchResponse {
  status: number;
  headers: {
    forEach?: (callback: (value: string, key: string) => void) => void;
  };
  text: () => Promise<string>;
}

export type FetchRequest = (
  input: string,
  init: FetchRequestInit,
) => Promise<FetchResponse>;

export interface AdminTransportOptions {
  baseUrl?: string;
  fetch?: FetchRequest;
  getCookie?: () => string;
  redirectToLogin?: () => void;
}

export class ApiRequestError<T = unknown> extends Error {
  readonly response: ApiResponse<T>;
  readonly statusCode: number;

  constructor(response: ApiResponse<T>) {
    super(`API request failed with status ${response.statusCode}`);
    this.name = "ApiRequestError";
    this.response = response;
    this.statusCode = response.statusCode;
  }
}

function appendQuery(url: string, query?: ApiRequest["query"]): string {
  if (!query) return url;
  const entries = Object.entries(query).filter(([, value]) => value !== undefined);
  if (entries.length === 0) return url;
  const separator = url.includes("?") ? "&" : "?";
  return `${url}${separator}${entries
    .map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`)
    .join("&")}`;
}

function joinUrl(baseUrl: string, path: string): string {
  if (/^https?:\/\//i.test(path)) return path;
  if (!baseUrl) return path;
  return `${baseUrl.replace(/\/+$/, "")}/${path.replace(/^\/+/, "")}`;
}

function readCookie(cookieHeader: string, name: string): string | undefined {
  const prefix = `${name}=`;
  const cookie = cookieHeader
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix));
  if (!cookie) return undefined;
  const value = cookie.slice(prefix.length);
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function defaultCookieReader(): string {
  return typeof document === "undefined" ? "" : document.cookie;
}

function isWrite(method: string): boolean {
  return !["GET", "HEAD", "OPTIONS"].includes(method);
}

function removeHeader(headers: Record<string, string>, name: string): void {
  const wanted = name.toLowerCase();
  for (const key of Object.keys(headers)) {
    if (key.toLowerCase() === wanted) delete headers[key];
  }
}

function setHeader(headers: Record<string, string>, name: string, value: string): void {
  removeHeader(headers, name);
  headers[name] = value;
}

function isAdminLogin(path: string): boolean {
  return path.replace(/^https?:\/\/[^/]+/i, "").split("?", 1)[0] === "/api/admin/v1/auth/login";
}

function isEncodedBody(body: unknown): boolean {
  if (typeof body === "string") return true;
  if (body === null || typeof body !== "object") return false;
  const constructors = ["FormData", "Blob", "ArrayBuffer", "URLSearchParams"];
  return constructors.some((name) => {
    const constructor = (globalThis as Record<string, unknown>)[name];
    return typeof constructor === "function" && body instanceof (constructor as Function);
  });
}

function normalizeHeaders(responseHeaders: FetchResponse["headers"]): Record<string, string> {
  const headers: Record<string, string> = {};
  responseHeaders.forEach?.((value, key) => {
    headers[key.toLowerCase() === "x-trace-id" ? "X-Trace-Id" : key] = value;
  });
  return headers;
}

function getTraceId(data: unknown, headers: Record<string, string>): string | undefined {
  if (data !== null && typeof data === "object") {
    const traceId = (data as { trace_id?: unknown }).trace_id;
    if (typeof traceId === "string") return traceId;
  }
  return headers["X-Trace-Id"];
}

async function decodeBody(response: FetchResponse, headers: Record<string, string>): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  const contentType = Object.entries(headers).find(([key]) => key.toLowerCase() === "content-type")?.[1] ?? "";
  if (contentType.includes("json")) return JSON.parse(text);
  return text;
}

function getDefaultFetch(): FetchRequest {
  if (typeof globalThis.fetch !== "function") {
    throw new Error("fetch must be supplied by the admin runtime");
  }
  return globalThis.fetch.bind(globalThis) as FetchRequest;
}

export function createAdminTransport(options: AdminTransportOptions = {}): ApiTransport {
  const fetchRequest = options.fetch ?? getDefaultFetch();
  const baseUrl = options.baseUrl ?? "";
  const getCookie = options.getCookie ?? defaultCookieReader;

  return async (input) => {
    const method = input.method.toUpperCase();
    const path = input.path;
    const headers = { ...(input.headers ?? {}) };
    removeHeader(headers, "Origin");
    if (input.body !== undefined && !isEncodedBody(input.body)) {
      headers["Content-Type"] ??= "application/json";
    }

    if (isWrite(method)) {
      removeHeader(headers, "X-CSRF-Token");
    }
    if (isWrite(method) && !isAdminLogin(path)) {
      const csrfToken = readCookie(getCookie(), "csrf_token");
      if (csrfToken !== undefined) setHeader(headers, "X-CSRF-Token", csrfToken);
    }

    const body = input.body === undefined
      ? undefined
      : isEncodedBody(input.body)
        ? input.body
        : JSON.stringify(input.body);
    const url = appendQuery(joinUrl(baseUrl, path), input.query);
    const response = await fetchRequest(url, {
      method,
      headers,
      body,
      credentials: "include",
      mode: "cors",
    });
    const responseHeaders = normalizeHeaders(response.headers);
    const unauthorized = response.status === 401;
    if (unauthorized) options.redirectToLogin?.();

    let data: unknown;
    try {
      data = await decodeBody(response, responseHeaders);
    } catch (cause) {
      if (unauthorized) {
        throw new ApiRequestError({
          data: undefined,
          statusCode: response.status,
          headers: responseHeaders,
          trace_id: getTraceId(undefined, responseHeaders),
        });
      }
      throw cause;
    }

    const apiResponse: ApiResponse = {
      data,
      statusCode: response.status,
      headers: responseHeaders,
      trace_id: undefined,
    };
    apiResponse.trace_id = getTraceId(apiResponse.data, responseHeaders);

    if (response.status < 200 || response.status >= 300) {
      throw new ApiRequestError(apiResponse);
    }
    return apiResponse;
  };
}
