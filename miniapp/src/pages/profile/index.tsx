/**
 * Profile page (main package, tabBar). Shows the logged-in buyer, the
 * server-owned points balance and entries to orders. Unauthenticated visitors
 * see the WeChat one-click login button; a 401 here clears the session and
 * falls back to the guest view without retrying.
 *
 * Avatar + nickname use the WeChat profile-completion capability: the avatar
 * is a native `button open-type="chooseAvatar"` (WeChat returns a temp path
 * that must be uploaded to our storage), the nickname is an `input
 * type="nickname"` (WeChat suggests the bound nickname above the keyboard).
 */

import { Button, Image, Input, Text, View } from "@tarojs/components";
import { useDidShow } from "@tarojs/taro";
import { useState } from "react";

import { describeApiError, withTraceId } from "../../lib/errors";
import { toLocalImageUrl } from "../../lib/image-url";
import { getPreferencesItem, setPreferencesItem } from "../../lib/preferences";
import { getTaro } from "../../lib/taro";
import { showErrorToast } from "../../lib/ui";
import { getMe, updateMe, uploadAvatar } from "../../services/auth";
import type { User } from "../../services/types";
import { authStore } from "../../stores/auth";

import "./index.css";

/** Order quick entries: labels follow the server status display mapping. */
const ORDER_ENTRIES: Array<{ key: string; label: string; icon: string }> = [
  { key: "paid", label: "待发货", icon: "time" },
  { key: "shipped", label: "已发货", icon: "send" },
  { key: "completed", label: "已完成", icon: "check-circle" },
];

const NOTIFY_KEY = "profile.notify";

export function ProfilePage() {
  const [loggedIn, setLoggedIn] = useState(false);
  const [user, setUser] = useState<User | null>(null);
  const [pointsText, setPointsText] = useState("");
  const [nickname, setNickname] = useState("");
  const [uploading, setUploading] = useState(false);
  const [savingNickname, setSavingNickname] = useState(false);
  // Local-only preference; no server counterpart exists for notification.
  const [notifyOn, setNotifyOn] = useState(
    () => getPreferencesItem(NOTIFY_KEY) !== "0",
  );

  useDidShow(() => {
    const state = authStore.getState();
    if (!state.accessToken) {
      setLoggedIn(false);
      setUser(null);
      setPointsText("");
      return;
    }
    setLoggedIn(true);
    setUser(state.user);
    setNickname(state.user?.nickname ?? "");
    setPointsText(state.user ? state.user.points_balance : "");
    getMe()
      .then((me) => {
        setUser(me);
        setNickname(me.nickname);
        setPointsText(me.points_balance);
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        if (presentation.kind === "unauthorized") {
          setLoggedIn(false);
          setUser(null);
          setPointsText("");
        }
      });
  });

  const handleLogin = () => {
    authStore
      .login()
      .then((me) => {
        setLoggedIn(true);
        setUser(me);
        setNickname(me.nickname);
        setPointsText(me.points_balance);
        getTaro().showToast({ title: "登录成功", icon: "success" });
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        getTaro().showModal({
          title: presentation.title,
          content: withTraceId(presentation),
          showCancel: false,
          confirmText: "知道了",
        });
      });
  };

  const handleLogout = () => {
    authStore.clearSession();
    setLoggedIn(false);
    setUser(null);
    setPointsText("");
    getTaro().showToast({ title: "已退出登录", icon: "none" });
  };

  // WeChat avatar picker: `e.detail.avatarUrl` is a temp path on the device;
  // upload it to the backend so the avatar survives on every device.
  const handleChooseAvatar = (event: { detail: { avatarUrl?: string } }) => {
    const tempPath = event.detail.avatarUrl;
    if (!tempPath || !user || uploading) return;
    const token = authStore.accessToken;
    if (!token) return;
    setUploading(true);
    uploadAvatar(tempPath, token)
      .then((url) => {
        const updated: User = { ...user, avatar_url: url };
        setUser(updated);
        authStore.applyUser(updated);
        getTaro().showToast({ title: "头像已更新", icon: "success" });
      })
      .catch((err: unknown) => {
        showErrorToast(err);
      })
      .finally(() => {
        setUploading(false);
      });
  };

  const handleSaveNickname = () => {
    if (!user || savingNickname) return;
    const trimmed = nickname.trim();
    if (!trimmed) {
      getTaro().showToast({ title: "昵称不能为空", icon: "none" });
      return;
    }
    if (trimmed === user.nickname) return;
    setSavingNickname(true);
    updateMe({ nickname: trimmed })
      .then((updated) => {
        setUser(updated);
        setNickname(updated.nickname);
        authStore.applyUser(updated);
        getTaro().showToast({ title: "昵称已保存", icon: "success" });
      })
      .catch((err: unknown) => {
        showErrorToast(err);
      })
      .finally(() => {
        setSavingNickname(false);
      });
  };

  const goTo = (url: string) => {
    getTaro().navigateTo({ url });
  };

  const handleNotifyChange = (event: { detail: { value: boolean } }) => {
    setNotifyOn(event.detail.value);
    setPreferencesItem(NOTIFY_KEY, event.detail.value ? "1" : "0");
  };

  const handleClearCache = () => {
    getTaro().showToast({ title: "暂无可清理的应用缓存", icon: "none" });
  };

  return (
    <View className="page">
      {loggedIn && user ? (
        <View className="mine">
          {/* 红→橙渐变头部卡：余额大数字 + 头像 + 昵称编辑 */}
          <View className="mine-hero">
            <View className="mine-hero__top">
              <View className="mine-hero__id">
                <View className="mine-hero__nickname-row">
                  <Input
                    className="mine-hero__nickname"
                    type="nickname"
                    placeholder="点击填写昵称"
                    value={nickname}
                    onInput={(event) => setNickname(event.detail.value)}
                  />
                  <View
                    className="mine-hero__save"
                    hoverClass="mine-hero__save--hover"
                    onClick={() => handleSaveNickname()}
                  >
                    {savingNickname ? "保存中" : "保存"}
                  </View>
                </View>
                <Text className="mine-hero__hint">
                  兑换扣减积分，退款实时回分
                </Text>
              </View>
              <View className="mine-hero__avatar-wrap">
                <Image
                  className="mine-hero__avatar"
                  src={toLocalImageUrl(user.avatar_url)}
                  mode="aspectFill"
                />
                {uploading ? (
                  <View className="mine-hero__avatar-uploading">
                    <t-loading theme="circular" size="36rpx" text="" />
                  </View>
                ) : null}
                {/* Transparent native button overlays the avatar; tapping opens
                 * the WeChat avatar picker. Kept separate from the Image so the
                 * button chrome cannot affect avatar rendering. */}
                <Button
                  className="mine-hero__avatar-btn"
                  openType="chooseAvatar"
                  onChooseAvatar={handleChooseAvatar}
                />
              </View>
            </View>
            <View className="mine-hero__balance-row">
              <Text className="mine-hero__balance">{pointsText}</Text>
              <Text className="mine-hero__unit">积分</Text>
              <Text className="mine-hero__pill">余额</Text>
            </View>
          </View>

          {/* 我的订单：三格快捷入口，全部跳订单页（无按状态计数数据） */}
          <View className="mine-card">
            <View
              className="mine-card__head"
              hoverClass="mine-cell--hover"
              onClick={() => goTo("/pages/orders/index")}
            >
              <Text className="mine-card__title">我的订单</Text>
              <Text className="mine-card__more">查看全部</Text>
              <t-icon name="chevron-right" size="32rpx" color="rgba(0,0,0,0.4)" />
            </View>
            <View className="mine-orders">
              {ORDER_ENTRIES.map((entry) => (
                <View
                  key={entry.key}
                  className="mine-orders__item"
                  hoverClass="mine-cell--hover"
                  onClick={() => goTo("/pages/orders/index")}
                >
                  <t-icon
                    name={entry.icon}
                    size="48rpx"
                    color="#0052d9"
                  />
                  <Text className="mine-orders__label">{entry.label}</Text>
                </View>
              ))}
            </View>
          </View>

          {/* 功能入口 */}
          <View className="mine-card">
            <View
              className="mine-cell"
              hoverClass="mine-cell--hover"
              onClick={() => goTo("/pages/address/index")}
            >
              <View className="mine-cell__icon">
                <t-icon name="location" size="40rpx" color="#0052d9" />
              </View>
              <Text className="mine-cell__title">收货地址</Text>
              <t-icon
                name="chevron-right"
                size="36rpx"
                color="rgba(0,0,0,0.4)"
              />
            </View>
            <View
              className="mine-cell"
              hoverClass="mine-cell--hover"
              onClick={() => goTo("/pages/points/index")}
            >
              <View className="mine-cell__icon">
                <t-icon name="money" size="40rpx" color="#0052d9" />
              </View>
              <Text className="mine-cell__title">积分明细</Text>
              <Text className="mine-cell__note mine-cell__note--red">
                {pointsText}
              </Text>
              <t-icon
                name="chevron-right"
                size="36rpx"
                color="rgba(0,0,0,0.4)"
              />
            </View>
          </View>

          {/* 设置组 */}
          <View className="mine-card">
            <View className="mine-cell">
              <t-icon name="secured" size="44rpx" color="rgba(0,0,0,0.9)" />
              <Text className="mine-cell__title">账号与安全</Text>
              <Text className="mine-cell__note">微信授权登录</Text>
            </View>
            <View className="mine-cell">
              <t-icon
                name="notification"
                size="44rpx"
                color="rgba(0,0,0,0.9)"
              />
              <Text className="mine-cell__title">消息通知</Text>
              <t-switch
                value={notifyOn}
                onChange={(event: { detail: { value: boolean } }) =>
                  handleNotifyChange(event)
                }
              />
            </View>
            <View
              className="mine-cell"
              hoverClass="mine-cell--hover"
              onClick={() => handleClearCache()}
            >
              <t-icon name="delete" size="44rpx" color="rgba(0,0,0,0.9)" />
              <Text className="mine-cell__title">清除缓存</Text>
              <t-icon
                name="chevron-right"
                size="36rpx"
                color="rgba(0,0,0,0.4)"
              />
            </View>
            <View className="mine-cell">
              <t-icon name="info-circle" size="44rpx" color="rgba(0,0,0,0.9)" />
              <Text className="mine-cell__title">关于我们</Text>
              <Text className="mine-cell__note">v0.1.0</Text>
            </View>
          </View>

          <View className="mine-logout">
            <t-button
              theme="danger"
              variant="outline"
              block
              onTap={() => handleLogout()}
            >
              退出登录
            </t-button>
          </View>
        </View>
      ) : (
        <View className="mine mine--guest">
          <View className="mine-guest">
            <Text className="text-muted">登录后即可使用积分下单</Text>
            <t-button
              theme="primary"
              size="large"
              block
              onTap={() => handleLogin()}
            >
              微信一键登录
            </t-button>
          </View>
        </View>
      )}
    </View>
  );
}

export default ProfilePage;
