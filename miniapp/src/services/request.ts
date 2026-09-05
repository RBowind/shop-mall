import type { ApiRequest, ApiResponse, ApiTransport } from "./generated/api.ts";

export interface TaroRequestOptions {
  url: string;
  method: string;
  data?: unknown;
  header: Record<string, string>;
  dataType: "json";
}

export interface TaroResponse<T = unknown> {
  statusCode: number;
  data: T;
  header?: Record<string, string>;
  headers?: Record<string, string>;
}

export type TaroRequest = (
  options: TaroRequestOptions,
) => Promise<TaroResponse>;

export interface MiniappTransportOptions {
  baseUrl?: string;
  request?: TaroRequest;
  getAccessToken?: () => string | null | undefined;
  clearSession?: () => void;
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

function hasHeader(headers: Record<string, string>, name: string): boolean {
  const wanted = name.toLowerCase();
  return Object.keys(headers).some((key) => key.toLowerCase() === wanted);
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

function normalizeHeaders(
  ...sources: Array<Record<string, string> | undefined>
): Record<string, string> {
  return Object.assign({}, ...sources);
}

function getTraceId(data: unknown, headers: Record<string, string>): string | undefined {
  if (data !== null && typeof data === "object") {
    const traceId = (data as { trace_id?: unknown }).trace_id;
    if (typeof traceId === "string") return traceId;
  }
  const header = Object.entries(headers).find(([name]) => name.toLowerCase() === "x-trace-id");
  return header?.[1];
}

function normalizeResponse<T>(response: TaroResponse<T>): ApiResponse<T> {
  const headers = normalizeHeaders(response.header, response.headers);
  return {
    data: response.data,
    statusCode: response.statusCode,
    headers,
    trace_id: getTraceId(response.data, headers),
  };
}

function getDefaultTaroRequest(): TaroRequest {
  const runtime = (globalThis as { Taro?: { request?: TaroRequest } }).Taro;
  if (!runtime?.request) {
    throw new Error("Taro.request must be supplied by the miniapp runtime");
  }
  return runtime.request.bind(runtime);
}

export function createMiniappTransport(
  options: MiniappTransportOptions = {},
): ApiTransport {
  const request = options.request ?? getDefaultTaroRequest();
  const baseUrl = options.baseUrl ?? "";

  return async (input) => {
    const headers = { ...(input.headers ?? {}) };
    const token = options.getAccessToken?.();
    removeHeader(headers, "Authorization");
    if (token) setHeader(headers, "Authorization", `Bearer ${token}`);
    if (input.body !== undefined && !hasHeader(headers, "content-type")) {
      headers["Content-Type"] = "application/json";
    }

    let response: ApiResponse;
    try {
      const rawResponse = await request({
        url: appendQuery(joinUrl(baseUrl, input.path), input.query),
        method: input.method.toUpperCase(),
        data: input.body,
        header: headers,
        dataType: "json",
      });
      response = normalizeResponse(rawResponse);
    } catch (cause) {
      const failedResponse = cause as Partial<TaroResponse> | undefined;
      if (failedResponse?.statusCode === 401) {
        options.clearSession?.();
        throw new ApiRequestError(normalizeResponse(failedResponse as TaroResponse));
      }
      throw cause;
    }

    if (response.statusCode === 401) {
      options.clearSession?.();
    }
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw new ApiRequestError(response);
    }
    return response;
  };
}
