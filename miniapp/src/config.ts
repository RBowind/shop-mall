/**
 * Runtime configuration.
 *
 * `API_BASE_URL` is the backend origin every request goes through. The local
 * default points at the compose backend (`HTTP_ADDR=:8080`, host-mapped port
 * 8080). A real deployment injects the HTTPS origin at build time:
 *
 *   - `TARO_APP_API_BASE` — replaced by the Taro CLI build (via `defineConfig`
 *     `env` / `process.env.TARO_APP_*` substitution), or
 *   - `globalThis.__API_BASE__` — set by the environment bootstrap before the
 *     bundle evaluates.
 *
 * WeChat requires an absolute `https://` request URL; the transport appends
 * every path to this origin.
 */

declare const process: { env: { TARO_APP_API_BASE?: string } } | undefined;

const envBase =
  typeof process !== "undefined" ? process.env.TARO_APP_API_BASE : undefined;
const runtimeBase = (globalThis as { __API_BASE__?: string }).__API_BASE__;

export const API_BASE_URL: string =
  envBase ?? runtimeBase ?? "http://127.0.0.1:8080";
