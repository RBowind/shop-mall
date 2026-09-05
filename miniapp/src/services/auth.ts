/**
 * Buyer authentication service.
 *
 * `wxLogin` exchanges a one-time WeChat code for a buyer JWT. The code is
 * single-use: the caller must obtain a fresh code via `wx.login` before every
 * call (see `src/lib/wx-login.ts`). `session_key` never reaches this client.
 */

import { getTransport } from "../lib/transport.ts";
import { getTaro } from "../lib/taro.ts";
import { API_BASE_URL } from "../config.ts";
import type { BuyerLoginResponse, User, UserResponse } from "./types.ts";

export interface WxLoginResult {
  accessToken: string;
  user: User;
}

export async function wxLogin(code: string): Promise<WxLoginResult> {
  const response = await getTransport()({
    method: "POST",
    path: "/api/v1/auth/wx-login",
    body: { code },
  });
  const envelope = response.data as BuyerLoginResponse;
  return {
    accessToken: envelope.data.access_token,
    user: envelope.data.user,
  };
}

export async function getMe(): Promise<User> {
  const response = await getTransport()({ method: "GET", path: "/api/v1/me" });
  return (response.data as UserResponse).data;
}

/** PATCH the buyer profile; only present fields are written server-side. */
export async function updateMe(patch: {
  nickname?: string;
  avatar_url?: string;
}): Promise<User> {
  const response = await getTransport()({
    method: "PATCH",
    path: "/api/v1/me",
    body: patch,
  });
  return (response.data as UserResponse).data;
}

/**
 * Upload the avatar picked by the WeChat chooseAvatar capability. The
 * multipart upload cannot go through the JSON transport, so it uses
 * wx.uploadFile directly with the bearer token. Returns the stored avatar URL.
 */
export async function uploadAvatar(
  filePath: string,
  accessToken: string,
): Promise<string> {
  const taro = getTaro();
  const result = await taro.uploadFile?.({
    url: `${API_BASE_URL}/api/v1/me/avatar`,
    filePath,
    name: "file",
    header: { Authorization: `Bearer ${accessToken}` },
  });
  if (!result) {
    throw new Error("当前环境不支持上传文件");
  }
  if (result.statusCode !== 201) {
    let message = `上传失败（${result.statusCode}）`;
    try {
      const payload = JSON.parse(result.data) as { message?: string };
      if (payload.message) message = payload.message;
    } catch {
      // non-JSON body; keep the generic message
    }
    throw new Error(message);
  }
  const payload = JSON.parse(result.data) as { data?: { url?: string } };
  const url = payload?.data?.url;
  if (!url) {
    throw new Error("上传响应缺少图片地址");
  }
  return url;
}