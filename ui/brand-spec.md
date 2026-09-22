# Brand Spec — 积分商城（shop-mall 微信小程序）

来源：用户关联代码仓库 `D:\workspace\shop-mall`（`miniapp/src/app.css`、`app.config.ts`、`docs/prd.md`）。以下色值全部从真实代码中提取并换算为 OKLch，非猜测。

> 本文件是**色值提取记录**（逐值标注出处）。完整设计系统——token 绑定规则、排版阶梯、间距圆角、组件清单、交互状态、手机壳与导航约定——见 **[DESIGN.md](DESIGN.md)**。

一句话概括：浅灰底 + 白卡的小程序商城界面，红色只负责「价格与选中强调」，蓝色只负责「可点击的动作」，积分是唯一货币单位。

## 六个核心 token

```css
:root {
  --bg:      oklch(96.5% 0 0);      /* #f5f5f5 页面底 */
  --surface: oklch(100% 0 0);       /* #ffffff 卡片 */
  --fg:      oklch(18% 0 0);        /* rgba(0,0,0,.9) 主文字 */
  --muted:   oklch(58% 0 0);        /* rgba(0,0,0,.5) 辅助文字 */
  --border:  oklch(93% 0 0);        /* rgba(0,0,0,.06) 分隔线 */
  --accent:  oklch(58% 0.19 25);    /* #e64340 品牌红：价格 / tabBar 选中 / 分类高亮 */
}
```

## 品牌功能色（同样来自 app.css，成对出现）

```css
:root {
  --action:      oklch(46% 0.23 264);  /* #0052d9 TDesign 主按钮蓝 / 订单筛选选中 */
  --action-soft: oklch(96% 0.018 255); /* #f0f6ff 选中背景 */
  --error:       oklch(52% 0.19 25);   /* #d54941 余额不足 / 错误 */
  --warn-bg:     oklch(96.5% 0.022 75);/* #fff3e8 提示条底 */
  --warn-fg:     oklch(50% 0.11 70);   /* #b26a00 提示条文字 */
}
```

## 字体

```css
--font-ui:   -apple-system, 'PingFang SC', 'HarmonyOS Sans SC', 'Microsoft YaHei', 'Segoe UI', sans-serif;
--font-mono: ui-monospace, 'SF Mono', 'JetBrains Mono', Menlo, Consolas, monospace;
```

小程序为工具型、数据密集界面，display/body 同为系统无衬线栈（即原型中的 `--font-ui`，不另设 display 字体变量）；所有积分数字走 `--font-mono` + tabular-nums。

## 观察到的规则（3–5 条）

> 下列 rpx/px 为 shop-mall 小程序原值；原型在 390px 画框内等比换算（卡片圆角取 12px 等），实现口径以 `DESIGN.md` + `assets/app.css` 为准。

1. **红 = 价格与强调，蓝 = 动作**：积分价格、tabBar 选中、分类选中竖条用品牌红；「立即兑换 / 去结算」等主按钮用 #0052d9 蓝。同一屏幕红蓝各司其职，不互相竞争。
2. **卡片语言**：白卡 8px 圆角落在 #f5f5f5 灰底上，分隔用 1px 低透明黑线，卡片几乎无阴影。
3. **价格格式**：大号数字 + 小号「积分」单位（48rpx 数字 + 26rpx 单位），永远不是 ¥ 元。
4. **决策页固定底栏**：详情/购物车/结算/订单详情有白色底部操作栏 + 安全区内边距，页面主体预留等高 padding。
5. **状态提示成对**：警示条 #fff3e8/#b26a00，危险态 #d54941（余额不足文字、退款驳回），选中态 #f0f6ff + #0052d9。

## 业务事实（原型内容依据）

- TabBar 四页：首页 / 分类 / 购物车 / 我的。
- 积分流水事件：注册赠送 +100、下单扣分、退款回分、管理员调整（append-only 账本）。
- 订单状态机：待发货(paid) → 已发货(shipped) → 已完成(completed)；paid → 申请退款(refund_requested) → 已退款(refunded)/驳回回 paid。
- 价格锚点：无线耳机 100 分、机械键盘 200 分、台灯 300 分（来自真实测试数据），其余商品自拟、标注为示意。
- 不实现：真实支付、优惠券、物流单号（PRD P2）。
