# DESIGN.md — 积分商城小程序原型 设计系统

本文件是本项目的**唯一设计系统事实来源**。所有屏幕（`screens/*.html`）与共享样式（`assets/app.css`、`assets/app.js`）都绑定这里定义的 token 与组件约定；改动设计先改本文件，再改实现。

- **品牌来源**：真实仓库 `D:\workspace\shop-mall`（`miniapp/src/app.css`、`app.config.ts`、`docs/prd.md`）。所有色值由原代码 hex 换算为 OKLch，逐值标注来源，见 `brand-spec.md`（色值提取记录）。
- **绑定规则**：任何页面与组件只能使用 `var(--*)`；裸 hex 仅允许出现在 `assets/app.css` 的 `:root` 注释中（记录来源），不得出现在样式值里。
- **一句话系统观**：浅灰底 + 白卡的小程序商城界面——**品牌红只负责「价格与选中」，操作蓝只负责「可点击的动作」**，积分是唯一货币单位。

---

## 1. Token（`assets/app.css` 的 `:root`）

### 品牌六色（基础）

| Token | 值 | 来源 hex（shop-mall） | 用途 |
|---|---|---|---|
| `--bg` | `oklch(96.5% 0 0)` | `#f5f5f5` | 页面底 |
| `--surface` | `oklch(100% 0 0)` | `#ffffff` | 卡片、弹层、底栏 |
| `--fg` | `oklch(18% 0 0)` | `rgba(0,0,0,.9)` | 主文字 |
| `--muted` | `oklch(58% 0 0)` | `rgba(0,0,0,.5)` | 辅助文字（≥12px 次级信息） |
| `--border` | `oklch(93% 0 0)` | `rgba(0,0,0,.06)` | 分隔线、描边 |
| `--accent` | `oklch(58% 0.19 25)` | `#e64340` | **价格、tabBar 选中、分类高亮** |

### 品牌功能色

| Token | 值 | 来源 hex | 用途 |
|---|---|---|---|
| `--action` | `oklch(46% 0.23 264)` | `#0052d9` | **主操作按钮、链接、筛选选中** |
| `--action-soft` | `oklch(96% 0.018 255)` | `#f0f6ff` | 选中态背景（与 `--action` 成对） |
| `--error` | `oklch(52% 0.19 25)` | `#d54941` | 余额不足、退款/危险提示 |
| `--warn-bg` | `oklch(96.5% 0.022 75)` | `#fff3e8` | 警示条底 |
| `--warn-fg` | `oklch(50% 0.11 70)` | `#b26a00` | 警示条文字（与 `--warn-bg` 成对） |

### 派生 token（计算得出，勿手工改）

`--accent-soft`（accent 12% 透明）、`--fg-2` / `--fg-4` / `--fg-6`（fg 的 70% / 42% / 62%）、`--hairline`（fg 6%）、`--tile`（fg 4% 叠 surface）。派生一律用 `color-mix(in oklch, …)`，不新增 `--accent-50/--accent-300` 之类的阶梯 token。

### 颜色语义（硬规则）

1. **红 = 价格与强调，蓝 = 动作**。同一屏内两者各司其职：价格用 `--accent`，可点按钮用 `--action`，不互相抢位。
2. **状态色成对出现**：选中 `--action-soft` + `--action`；警示 `--warn-bg` + `--warn-fg`；错误 `--error`（配 8–10% 透明底）。前景与背景永远同规则给出，不做单边替换。
3. **hover 不降对比度**：背景在 L 通道移动 ±0.06–0.12，或改描边/阴影；禁止把文字在 hover 时改成更浅的颜色。
4. **对比度门槛**：正文对背景 ≥ 4.5:1，大字（≥18px）与图标 ≥ 3:1；`--muted` 只用于 ≥12px 的次级信息；`disabled` 是唯一允许降低对比度的状态。

---

## 2. 排版

```css
--font-ui:   -apple-system, 'PingFang SC', 'HarmonyOS Sans SC', 'Microsoft YaHei', 'Segoe UI', sans-serif;
--font-mono: ui-monospace, 'SF Mono', 'JetBrains Mono', Menlo, Consolas, monospace;
```

- 小程序为工具型、数据密集界面，**display 与 body 同为系统无衬线栈**（不引入衬线/装饰字体）；中文按 PingFang → HarmonyOS → 雅黑 回退，字体链写全。
- **数字一律走 `--font-mono` + `font-variant-numeric: tabular-nums`**（`.tnum` 或 `.price`）：积分、价格、订单号、日期、数量。数字与单位用 `nowrap` 成对，避免断行。
- 原型实际使用的字号阶梯（px）：`10`（角标/极小注）· `11`（辅助说明）· `12`（次级信息）· `13`（密集正文）· `14`（正文）· `15`（按钮）· `16`（卡片标题）· `17`（导航标题）· `18–20`（强调数值）· `22–28`（余额/积分大数）· `36–40`（hero 级数值）。
- 行高：正文 1.5–1.7；标题 1.3–1.4；长段落加 `text-wrap: pretty`，标题 `text-wrap: balance`。
- 字重只用两档：`600/700`（标题、数值、按钮）与 `400`（正文）。不使用斜体。

---

## 3. 间距、圆角、阴影

- **间距节奏**（8pt 近似）：`4 · 6 · 8 · 10 · 12 · 14 · 16 · 18 · 20 · 24` px。卡片内边距常用 14–16，卡片间距 10–12，区块间距 12–18。
- **圆角**：`--r-card: 12px`（卡片、弹层内容块）、`--r-chip: 999px`（chip、主按钮、胶囊、搜索条）、`8px`（缩略图/小图标底）、`4px`（状态 pill、规格标签）。
- **阴影**：卡片默认无阴影（仅靠灰底与发丝线分层）。允许的两处投影——悬浮层的卡片 hover `0 6px 18px fg 10%`、品牌色 hero 卡 `0 8–10px 20–24px accent 26–30%`。**不做玻璃拟态、不做霓虹发光、不用大圆角卡片堆叠做装饰。**

---

## 4. 图标

- 单一线性图标族：`24 viewBox`、`stroke-width: 1.6`、`stroke-linecap/linejoin: round`、`fill: none`、颜色继承 `currentColor`；由 `assets/app.js` 的 `PS.icon(name, size)` 统一输出，**禁止 emoji 充当功能图标**。
- 尺寸档：`12–14`（行内/箭头）· `16–18`（cell、按钮内）· `22–24`（导航、tabBar）· `26–40`（占位图内产品图标）· `96`（详情页主图占位）。
- tabBar 图标尺寸统一 26，与实际文字标签成对出现；选中态用 `--accent` 着色。

---

## 5. 组件清单（类名 → 用途 → 状态）

| 组件 | 类名 | 关键状态 |
|---|---|---|
| 卡片 | `.card` · `.block`（间距钩子） · `.hairline` | — |
| 主按钮 | `.btn` · `.btn-primary` | hover 加深至 `action 86%+black`；`:active` 下移 1px；`.is-disabled` 灰底 + `pointer-events: none` |
| 次按钮 | `.btn-secondary` | hover → `--action-soft` 底 |
| 幽灵按钮 | `.btn-ghost` · `.btn-sm` · `.btn-block` | hover → `--action` 文字 + 描边 |
| 价格 | `.price` · `.price .unit` | 单位 0.55em，红色，mono |
| 标签切换 | `.chip` · `.chip.on` | 选中 `--accent-soft` 底 + 红字 + 600 字重 |
| 状态标签 | `.pill` · `.pill.warn` · `.pill.error` · `.pill.ok` | 与订单状态机一一对应 |
| 提示条 | `.notice` · `.notice.error` | 图标 + 文案，警示/危险两态 |
| 搜索条 | `.searchbar` | `:focus-within` 出 `--accent-soft` 焦点环 |
| 占位图 | `.ph-img` · `.ph-tag` | 缺图时用品牌色微渐变 + 产品图标 + `images[0].jpg` 文件标注，**不留灰色空盒** |
| 商品卡 | `.product-grid` · `.product-card` · `.pc-name/.pc-meta/.pc-stock` · `.soldout` · `.soldout-flag` | 售罄：图 45% 透明 + 名称转灰 + 圆形「已兑完」 |
| 列表单元 | `.cell-group` · `.cell` · `.cell-icon/.cell-main/.cell-title/.cell-sub/.cell-value/.chev` | hover 底色微变；`:focus-visible` 内描边 |
| 勾选 | `.cbx` · `.cbx.on` · `.c-accent` | 20px 圆形，选中填充 `--action`；`[disabled]` 灰底 |
| 步进器 | `.stepper` · `.qty` | 到边界时按钮 `[disabled]`；`:focus-within` 出焦点环 |
| 底部操作栏 | `.actionbar` · `.ab-total` · `.balance-hint` · `.insufficient` | 决策页固定底栏；余额不足时提示转 `--error` 且主按钮置灰 |
| 头像 | `.avatar` | 无图时用首字，`--tile` 底 |
| 弹层 | `.overlay` · `.dialog`（`.dg-body/.dg-title/.dg-text/.dg-actions`） | 遮罩 45% fg；左右双按钮，右侧为主操作（`--action`） |
| 轻提示 | `.toast` | 1.6s 自动消失，底部 120px 处 |
| 空态 | `.empty` · `.empty-art` | 说明为什么空 + 下一步入口（如「去逛逛」） |
| 筛选分段 | `.seg-tabs` · `button.on` | 选中 `--action-soft` + 蓝字 600 |
| 积分流水行 | `.ledger-row` · `.lg-main/.lg-title/.lg-sub` · `.lg-delta` · `.d.plus/.d.minus` · `.after` | 获得蓝 `+`、支出红 `−`，并显余额快照 |

---

## 6. 手机壳与屏幕骨架

- 画框：宽 `390px`、高 `844px`、外壳 padding `10px`、外圆角 `48px` / 屏幕圆角 `38px`；`home indicator` 134×5，距底 6px。
- 骨架高度常量：**状态栏 44 / 导航栏 44 / tabBar 62（含 6px 底部内边距）**；中段 `.body` 独立滚动（`overscroll-behavior: contain`，隐藏滚动条）。固定底栏必须为内容预留等高 padding。
- ≤ `440px` 视口（真机演示）：去壳全宽、`100dvh`，隐藏画框装饰，页面不得横向滚动。
- Chrome 渲染统一由 `PS.chrome({ title, back, tab })` 挂载到页面占位节点：`#chrome-top`（状态栏+导航栏）、`#chrome-tab`（tabBar）。**带 `tab` 的页面必须有 `#chrome-tab` 占位，否则导航会整条丢失。**

---

## 7. 导航约定

- 底部 tabBar 固定 4 项：**首页 / 分类 / 购物车 / 我的**，图标 + 文字，选中 `--accent`；购物车带数量角标。
- 子页（商品详情、兑换确认、成功、我的积分、订单、搜索）用导航栏左上返回键；返回目标由 `PS.chrome({ back })` 指定，页面间不出现死路。
- 主链路：`首页 → 商品详情 → 购物车 → 兑换确认 → 兑换成功 → 兑换订单`；分支：`我的 → 我的积分`。订单状态机：`待发货 → 已发货 → 已完成`，待发货可申请退款 → 已退款（积分退回并写入流水）。

---

## 8. 内容与数据口径

- 货币单位永远是**「积分」**，不出现 `¥`；价格格式为「大号数字 + 小号『积分』」。
- 商品名、库存、流水、订单均为**与业务自洽的示意数据**：注册赠 100 分（真实规则）、消费回馈、append-only 流水、幂等下单（`client_token`）。
- 未实拍的商品图以带文件名标注的占位图呈现；不伪造品牌素材与真实评测。示意/扩展内容必须在页内就地标注。

---

## 9. 不要做

- ✗ 裸 hex 出现在 `:root` 之外；✗ 新增调色板或第二套强调色。
- ✗ 紫色的渐变铺底、玻璃拟态、霓虹光、每层都加渐变；✗ 无业务理由的大圆角卡片堆叠。
- ✗ emoji 当图标；✗ hover 时把文字调浅；✗ 同一动作在一屏里出现多个实心主按钮。
-  固定底栏盖住内容（必须预留 padding）；✗ 横向滚动；✗ 用 `scrollIntoView`（会破坏预览 iframe）。
- ✗ 暴露设计者/演示者面板（视口切换、平台开关、设计元信息）——那是工具，不是产品 UI。

---

## 10. 文件地图

| 路径 | 角色 |
|---|---|
| `DESIGN.md` | 本文件：设计系统事实来源 |
| `brand-spec.md` | 色值提取记录（逐值来源追溯） |
| `assets/app.css` | token 实现 + 手机壳 + 全部组件类 |
| `assets/app.js` | 数据模型（商品/积分流水/订单/地址）+ chrome 渲染 + 图标库 |
| `index.html` | 总览启动页（10 屏缩略图 + 主链路流程 + 口径说明） |
| `screens/*.html` | 10 个独立屏幕：home / category / cart / mine / product-detail / checkout / success / points / orders / search |

> 改动流程：改 `DESIGN.md` → 改 `assets/app.css`（若涉及 token）→ 改受影响屏幕。三者必须保持一致。