/**
 * Taro App entry (React). Wires the singleton transport to the auth store:
 * requests carry the current Bearer token and a 401 response clears the
 * session exactly once (the transport never retries a failed request, so the
 * gate cannot spin).
 *
 * The API origin comes from `src/config.ts` (build-injected
 * `TARO_APP_API_BASE`, local default `http://127.0.0.1:8080`).
 */

import Taro, { useLaunch } from "@tarojs/taro";
import type { PropsWithChildren } from "react";

import { API_BASE_URL } from "./config";
import { installRuntimeTaro } from "./lib/taro";
import { configureTransport, createAppTransport } from "./lib/transport";
import { authStore } from "./stores/auth";
import "./app.css";

// The logic layer (`lib/taro.ts` getTaro) reads the Taro runtime from
// `globalThis.Taro`. In the WeChat runtime nothing supplies it, so install the
// real `@tarojs/taro` here before any module-scope code touches storage or
// login. Tests inject a fake via `setTaroForTest` instead.
installRuntimeTaro(Taro as Parameters<typeof installRuntimeTaro>[0]);

// Module-scope wiring runs once before the first render. The session restore
// reads the persisted token so a return visit is already logged in.
authStore.restore();

configureTransport(
  createAppTransport(
    API_BASE_URL,
    () => authStore.getState().accessToken,
    () => authStore.clearSession(),
  ),
);

function App({ children }: PropsWithChildren) {
  useLaunch(() => {
    // Session restore already happened at module scope; nothing else to seed.
  });
  return children;
}

export default App;