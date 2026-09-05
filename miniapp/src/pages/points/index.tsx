/**
 * Points ledger page (subpackage). Requires login.
 *
 * Red hero card with the server-owned balance, an append-only flow list
 * (+entries blue, -entries red) and a footer note. Loads on every show via
 * `useDidShow`; the ledger and balance both come straight from the server.
 */

import { Text, View } from "@tarojs/components";
import { useDidShow } from "@tarojs/taro";
import { useState } from "react";

import { describeApiError } from "../../lib/errors";
import { getMe } from "../../services/auth";
import { listPointsLedger } from "../../services/points";
import type { LedgerEntry, LedgerEntryType } from "../../services/points";
import { authStore } from "../../stores/auth";

import "./index.css";

const LEDGER_META: Record<LedgerEntryType, { title: string; icon: string }> = {
  signup_bonus: { title: "注册赠送", icon: "gift" },
  order_pay: { title: "兑换扣减", icon: "cart" },
  order_refund: { title: "退款回分", icon: "refresh" },
  admin_adjust: { title: "管理员调整", icon: "swap" },
};

/** ISO timestamp → "YYYY-MM-DD" in local time; empty if absent. */
function formatDay(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => (n < 10 ? `0${n}` : String(n));
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

/** int64-as-string delta → signed display text; stays in string land. */
function deltaText(delta: string): { sign: string; abs: string; plus: boolean } {
  if (delta.startsWith("-")) return { sign: "−", abs: delta.slice(1), plus: false };
  return { sign: "+", abs: delta, plus: true };
}

export function PointsPage() {
  const [entries, setEntries] = useState<LedgerEntry[]>([]);
  const [balance, setBalance] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorText, setErrorText] = useState("");

  const load = () => {
    setLoading(true);
    setErrorText("");
    Promise.all([getMe(), listPointsLedger({ page: 1, page_size: 50 })])
      .then(([me, list]) => {
        setBalance(me.points_balance);
        setEntries(list);
        setLoading(false);
      })
      .catch((err: unknown) => {
        const presentation = describeApiError(err);
        setLoading(false);
        setErrorText(presentation.message);
      });
  };

  useDidShow(() => {
    if (!authStore.requireLogin()) return;
    load();
  });

  return (
    <View className="page points">
      {/* 余额卡：红渐变底 + 白色大数字（points.html hero） */}
      <View className="points-hero">
        <Text className="points-hero__label">可用积分</Text>
        <View className="points-hero__row">
          <Text className="points-hero__balance">{balance || "--"}</Text>
          <Text className="points-hero__unit">积分</Text>
        </View>
      </View>

      <Text className="points-section-title">积分流水</Text>

      {errorText ? (
        <View className="error-block">
          <Text className="text-error">{errorText}</Text>
          <t-button theme="primary" variant="outline" size="small" onTap={() => load()}>
            重试
          </t-button>
        </View>
      ) : null}

      {loading && entries.length === 0 ? (
        <View className="loading-block">
          <t-loading theme="circular" size="48rpx" text="加载中…" />
        </View>
      ) : null}

      {entries.length > 0 ? (
        <View className="points-card">
          {entries.map((entry) => {
            const meta =
              LEDGER_META[entry.type] ?? { title: "积分变动", icon: "money" };
            const delta = deltaText(entry.delta);
            return (
              <View key={entry.id} className="points-row">
                <View className="points-row__icon">
                  <t-icon name={meta.icon} size="36rpx" color="rgba(0,0,0,0.6)" />
                </View>
                <View className="points-row__main">
                  <Text className="points-row__title">{meta.title}</Text>
                  <Text className="points-row__date">
                    {formatDay(entry.created_at)}
                  </Text>
                </View>
                <View className="points-row__right">
                  <Text
                    className={
                      delta.plus
                        ? "points-row__delta points-row__delta--plus"
                        : "points-row__delta points-row__delta--minus"
                    }
                  >
                    {delta.sign}
                    {delta.abs}
                  </Text>
                  <Text className="points-row__after">
                    余额 {entry.balance_after}
                  </Text>
                </View>
              </View>
            );
          })}
        </View>
      ) : null}

      {!loading && !errorText && entries.length === 0 ? (
        <View className="empty-block">
          <t-empty description="暂无积分流水" />
        </View>
      ) : null}

      <Text className="points-footnote">退款回分实时到账</Text>
    </View>
  );
}

export default PointsPage;
