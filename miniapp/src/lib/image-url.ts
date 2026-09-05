/**
 * Local-acceptance image origin rewrite.
 *
 * The backend stamps every image URL with `PUBLIC_BASE_URL`, which the config
 * loader forces to be HTTPS. In the local acceptance topology that origin is
 * the admin dev server's self-signed certificate (https://localhost:8000): the
 * logic layer downloads it fine (wx.getImageInfo succeeds), but the render-layer
 * <Image> host can reject the self-signed TLS handshake, so galleries may
 * render blank.
 *
 * When the app itself runs against a local API origin (127.0.0.1 / localhost),
 * image URLs are re-based onto that origin — the same backend serves /static
 * in dev, so images load over plain HTTP. Against a real deployment origin
 * the function is an identity pass-through, so production behavior is
 * unchanged.
 */

import { API_BASE_URL } from "../config";

function isLocalApiOrigin(origin: string): boolean {
  return origin.includes("127.0.0.1") || origin.includes("localhost");
}

export function toLocalImageUrl(url: string): string {
  if (!url) return url;
  try {
    const source = new URL(url);
    const target = new URL(API_BASE_URL);
    if (!isLocalApiOrigin(target.origin)) return url;
    if (source.origin === target.origin) return url;
    return `${target.origin}${source.pathname}${source.search}`;
  } catch {
    return url;
  }
}
