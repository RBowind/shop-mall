/* ============================================================
   积分商城原型 · 共享数据与逻辑
   数据模型对齐 shop-mall 后端（0001_init.up.sql）：
   points_ledger(type: signup_bonus|order_pay|order_refund|
   admin_adjust) / orders(status: paid|shipped|completed|
   refund_requested|refunded) / user_addresses / 注册赠 100 分。
   ============================================================ */

var PS = (function () {
  'use strict';

  /* ── 商品目录（digital/home 真实测试数据为锚，其余为示意） ── */
  var PRODUCTS = [
    { id: 'earbuds',    name: '无线降噪耳机',        cat: '数码', points: 100, stock: 20, sold: 328, icon: 'headphone', specs: ['曜岩黑', '云母白'] },
    { id: 'keyboard',   name: '机械键盘 87键 红轴',  cat: '数码', points: 200, stock: 12, sold: 156, icon: 'keyboard',  specs: ['红轴', '茶轴'] },
    { id: 'lamp',       name: 'LED 护眼台灯',        cat: '家居', points: 300, stock: 8,  sold: 94,  icon: 'lamp',      specs: ['暖光', '冷光'] },
    { id: 'mug',        name: '陶瓷保温马克杯',      cat: '家居', points: 150, stock: 30, sold: 210, icon: 'mug',       specs: ['雾霾蓝', '燕麦白'] },
    { id: 'candle',     name: '香薰蜡烛 檀香',       cat: '家居', points: 90,  stock: 25, sold: 178, icon: 'candle',    specs: ['檀香', '海盐'] },
    { id: 'tote',       name: '帆布托特包',          cat: '生活', points: 20,  stock: 50, sold: 642, icon: 'bag',       specs: ['原色'] },
    { id: 'speaker',    name: '便携蓝牙音箱',        cat: '数码', points: 350, stock: 5,  sold: 41,  icon: 'speaker',   specs: ['深灰'] },
    { id: 'lunchbox',   name: '三层不锈钢饭盒',      cat: '生活', points: 120, stock: 18, sold: 123, icon: 'box',       specs: ['本色'] },
    { id: 'stand',      name: '折叠手机支架',        cat: '数码', points: 40,  stock: 0,  sold: 500, icon: 'stand',     specs: ['银色'] },
    { id: 'bottle',     name: '保温运动水壶 500ml',  cat: '生活', points: 60,  stock: 3,  sold: 267, icon: 'bottle',    specs: ['墨绿', '雾粉'] }
  ];
  var CATS = ['全部', '数码', '家居', '生活'];

  /* ── 用户与积分账本（余额 460，与流水自洽，见 brand-spec） ── */
  var USER = {
    nickname: '雨天的小店',
    initials: '雨',
    joined: '2026-07-01',
    addresses: [
      { id: 1, receiver: '王小雨', phone: '138****6204', region: '浙江省 杭州市 西湖区', detail: '文三路 100 号 数娱大厦 A 座 12 层', isDefault: true, version: 2 },
      { id: 2, receiver: '王小雨', phone: '138****6204', region: '浙江省 杭州市 拱墅区', detail: '上塘路 88 号 3 幢 2 单元 501', isDefault: false, version: 1 }
    ]
  };
  var LEDGER = [
    { date: '08-29 12:40', type: 'order_pay',      title: '订单消费回馈', delta: 200,  after: 460, remark: '订单 SN20260829-0051' },
    { date: '08-27 20:15', type: 'exchange',       title: '兑换订单扣分', delta: -100, after: 260, remark: '无线降噪耳机 ×1 · SN20260827-0043' },
    { date: '08-23 19:02', type: 'exchange',       title: '兑换订单扣分', delta: -300, after: 360, remark: 'LED 护眼台灯 ×1 · SN20260823-0038' },
    { date: '08-20 10:11', type: 'exchange',       title: '兑换订单扣分', delta: -150, after: 660, remark: '陶瓷保温马克杯 ×1 · SN20260820-0027' },
    { date: '08-19 21:36', type: 'order_pay',      title: '订单消费回馈', delta: 80,   after: 810, remark: '订单 SN20260819-0019' },
    { date: '08-05 15:08', type: 'order_pay',      title: '订单消费回馈', delta: 450,  after: 730, remark: '订单 SN20260805-0008' },
    { date: '07-14 09:52', type: 'order_pay',      title: '订单消费回馈', delta: 200,  after: 280, remark: '订单 SN20260714-0004' },
    { date: '07-10 18:47', type: 'exchange',       title: '兑换订单扣分', delta: -20,  after: 80,  remark: '帆布托特包 ×1 · SN20260710-0002' },
    { date: '07-01 09:20', type: 'signup_bonus',   title: '注册赠送',     delta: 100,  after: 100, remark: '首次登录自动赠送 · 仅一次' }
  ];
  var ORDERS = [
    { no: 'SN20260827-0043', pid: 'earbuds',  qty: 1, points: 100, status: 'paid',             created: '08-27 20:15', shipped: null },
    { no: 'SN20260823-0038', pid: 'lamp',     qty: 1, points: 300, status: 'shipped',          created: '08-23 19:02', shipped: '08-28 11:00' },
    { no: 'SN20260820-0027', pid: 'mug',      qty: 1, points: 150, status: 'refund_requested', created: '08-20 10:11', shipped: '08-22 09:30', refundReason: '收到时有磕碰缺口' },
    { no: 'SN20260710-0002', pid: 'tote',     qty: 1, points: 20,  status: 'completed',        created: '07-10 18:47', shipped: '07-12 10:00' }
  ];

  var STATUS_LABEL = { paid: '待发货', shipped: '已发货', completed: '已完成', refund_requested: '退款审核中', refunded: '已退款' };

  /* ── 本地持久化（刷新后状态保留，模拟真实会话） ── */
  function ls(key, fallback) {
    try {
      var raw = localStorage.getItem('ps-' + key);
      return raw ? JSON.parse(raw) : fallback;
    } catch (e) { return fallback; }
  }
  function lsSet(key, val) { try { localStorage.setItem('ps-' + key, JSON.stringify(val)); } catch (e) {} }

  function getBalance() { return ls('balance', 460); }
  function setBalance(n) { lsSet('balance', n); }
  function getLedger() { return ls('ledger', LEDGER); }
  function addLedger(entry) {
    var l = getLedger();
    l.unshift(entry);
    lsSet('ledger', l);
  }

  function getCart() {
    return ls('cart', [
      { id: 'keyboard', qty: 1, selected: true },
      { id: 'mug',      qty: 1, selected: true },
      { id: 'candle',   qty: 2, selected: false }
    ]);
  }
  function setCart(c) { lsSet('cart', c); }
  function cartCount() { return getCart().length; }

  function getOrders() { return ls('orders', ORDERS); }
  function saveOrders(o) { lsSet('orders', o); }

  function productById(id) {
    for (var i = 0; i < PRODUCTS.length; i++) if (PRODUCTS[i].id === id) return PRODUCTS[i];
    return null;
  }
  function fmt(n) { return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ','); }

  /* ── 线性图标（24 viewBox, stroke 1.6, 继承 currentColor） ── */
  var P = 'stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" fill="none"';
  var ICONS = {
    headphone: '<path ' + P + ' d="M4 14v-2a8 8 0 0 1 16 0v2"/><rect x="3" y="14" width="4" height="6" rx="1.5" ' + P + '/><rect x="17" y="14" width="4" height="6" rx="1.5" ' + P + '/>',
    keyboard:  '<rect x="2.5" y="7" width="19" height="11" rx="2" ' + P + '/><path ' + P + ' d="M6 10h.01M9.5 10h.01M13 10h.01M16.5 10h.01M6 14h.01M8 14.5h8"/>',
    lamp:      '<path ' + P + ' d="M9 3h4l4 8H5L9 3z"/><path ' + P + ' d="M11 11v7"/><path ' + P + ' d="M8 21h7"/>',
    mug:       '<path ' + P + ' d="M5 7h11v9a3 3 0 0 1-3 3H8a3 3 0 0 1-3-3V7z"/><path ' + P + ' d="M16 9h2.5a2.5 2.5 0 0 1 0 5H16"/>',
    candle:    '<rect x="8" y="11" width="8" height="9" rx="1.5" ' + P + '/><path ' + P + ' d="M12 11V8"/><path ' + P + ' d="M12 8c0-1.6-1.2-2-1.2-3.2C10.8 3.4 12 3 12 3s1.2.4 1.2 1.8C13.2 6 12 6.4 12 8z"/>',
    bag:       '<path ' + P + ' d="M5 8h14l-1.2 12H6.2L5 8z"/><path ' + P + ' d="M9 8V6a3 3 0 0 1 6 0v2"/>',
    speaker:   '<rect x="6" y="3.5" width="12" height="17" rx="2.5" ' + P + '/><circle cx="12" cy="14" r="3.5" ' + P + '/><circle cx="12" cy="7" r="1" ' + P + '/>',
    box:       '<rect x="4" y="9" width="16" height="4" rx="1" ' + P + '/><rect x="5" y="13" width="14" height="4" rx="1" ' + P + '/><rect x="6" y="17" width="12" height="3.5" rx="1" ' + P + '/><path ' + P + ' d="M8 9V6a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v3"/>',
    stand:     '<rect x="8" y="3" width="11" height="15" rx="1.5" ' + P + ' transform="rotate(10 13 10)"/><path ' + P + ' d="M4 21l6-4"/>',
    bottle:    '<path ' + P + ' d="M10 3h4v3l1.5 2.5V21h-7V8.5L10 6V3z"/><path ' + P + ' d="M8.5 12h7"/>',
    coin:      '<circle cx="12" cy="12" r="8.5" ' + P + '/><circle cx="12" cy="12" r="3.5" ' + P + '/>',
    gift:      '<rect x="4" y="10" width="16" height="10" rx="1.5" ' + P + '/><path ' + P + ' d="M4 10V8a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v2M12 6v14M12 6c-1.5-2.5-5-2.5-5 0M12 6c1.5-2.5 5-2.5 5 0"/>',
    search:    '<circle cx="11" cy="11" r="6.5" ' + P + '/><path ' + P + ' d="M16 16l4.5 4.5"/>',
    back:      '<path ' + P + ' d="M15 5l-7 7 7 7"/>',
    chev:      '<path ' + P + ' d="M9 5l7 7-7 7"/>',
    close:     '<path ' + P + ' d="M6 6l12 12M18 6L6 18"/>',
    check:     '<path ' + P + ' d="M4.5 12.5l5 5L19.5 7"/>',
    cart:      '<path ' + P + ' d="M3.5 5h2l2.2 11.2a1.6 1.6 0 0 0 1.6 1.3h8.1a1.6 1.6 0 0 0 1.55-1.2L20.5 8H6.3"/><circle cx="9.5" cy="20" r="1.2" ' + P + '/><circle cx="17" cy="20" r="1.2" ' + P + '/>',
    home:      '<path ' + P + ' d="M4 11.5 12 4l8 7.5M6.5 9.5V20h11V9.5"/>',
    grid:      '<rect x="4" y="4" width="7" height="7" rx="1.5" ' + P + '/><rect x="13" y="4" width="7" height="7" rx="1.5" ' + P + '/><rect x="4" y="13" width="7" height="7" rx="1.5" ' + P + '/><rect x="13" y="13" width="7" height="7" rx="1.5" ' + P + '/>',
    user:      '<circle cx="12" cy="8" r="3.6" ' + P + '/><path ' + P + ' d="M5 20c.8-3.8 3.6-5.6 7-5.6s6.2 1.8 7 5.6"/>',
    pin:       '<path ' + P + ' d="M12 21s-7-6-7-11a7 7 0 0 1 14 0c0 5-7 11-7 11z"/><circle cx="12" cy="10" r="2.5" ' + P + '/>',
    doc:       '<rect x="5" y="3.5" width="14" height="17" rx="2" ' + P + '/><path ' + P + ' d="M8.5 8h7M8.5 12h7M8.5 16h4.5"/>',
    refresh:   '<path ' + P + ' d="M20 11a8 8 0 1 0-.8 4.5"/><path ' + P + ' d="M20 5v6h-6"/>',
    headset:   '<path ' + P + ' d="M4 13v-1a8 8 0 0 1 16 0v1"/><rect x="3" y="13" width="4" height="7" rx="1.6" ' + P + '/><rect x="17" y="13" width="4" height="7" rx="1.6" ' + P + '/>',
    info:      '<circle cx="12" cy="12" r="8.5" ' + P + '/><path ' + P + ' d="M12 11v5M12 8h.01"/>',
    trash:     '<path ' + P + ' d="M4.5 7h15M9.5 7V4.5h5V7M6.5 7l1 13h9l1-13"/>',
    arrow:     '<path ' + P + ' d="M5 12h14M13 6l6 6-6 6"/>',
    minus:     '<path ' + P + ' d="M6 12h12"/>',
    plus:      '<path ' + P + ' d="M12 6v12M6 12h12"/>'
  };
  function icon(name, size) {
    return '<svg viewBox="0 0 24 24" width="' + (size || 16) + '" height="' + (size || 16) + '" aria-hidden="true">' + ICONS[name] + '</svg>';
  }

  /* ── 手机画框 chrome：状态栏 / 导航栏 / tabbar / home 指示条 ── */
  var sigSvg = '<svg viewBox="0 0 18 12" width="17" height="11" fill="currentColor" aria-hidden="true"><rect x="0" y="8" width="3" height="4" rx="1"/><rect x="5" y="5.5" width="3" height="6.5" rx="1"/><rect x="10" y="3" width="3" height="9" rx="1"/><rect x="15" y="0.5" width="3" height="11.5" rx="1"/></svg>';
  var wifiSvg = '<svg viewBox="0 0 16 12" width="15" height="11" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" aria-hidden="true"><path d="M1.5 4.2a9.5 9.5 0 0 1 13 0M3.8 6.8a6.2 6.2 0 0 1 8.4 0M6.2 9.3a3 3 0 0 1 3.6 0"/><circle cx="8" cy="11" r="0.9" fill="currentColor" stroke="none"/></svg>';
  var battSvg = '<svg viewBox="0 0 27 12" width="25" height="11" aria-hidden="true"><rect x="0.6" y="0.6" width="22" height="10.8" rx="3" fill="none" stroke="currentColor" stroke-width="1.2" opacity="0.5"/><rect x="2.4" y="2.4" width="16" height="7.2" rx="1.8" fill="currentColor"/><path d="M24.6 4.5v3a2.4 2.4 0 0 0 0-3z" fill="currentColor" opacity="0.5"/></svg>';

  function statusBar() {
    return '<div class="statusbar"><span class="tnum" style="font-weight:700">9:41</span>' +
      '<span class="sb-right">' + sigSvg + wifiSvg + battSvg + '</span></div>';
  }
  function capsule() {
    return '<div class="capsule" aria-hidden="true">' +
      '<span><svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M8 12l2.4 2.4L16 8.8"/></svg></span>' +
      '<span><svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg></span></div>';
  }
  function nav(opts) {
    var back = opts.back
      ? '<a class="nav-back" href="' + opts.back + '" aria-label="返回">' + icon('back', 22) + '</a>' : '';
    return '<div class="navbar">' + back + '<span class="nav-title">' + opts.title + '</span>' + capsule() + '</div>';
  }
  function tabBar(active) {
    var tabs = [
      { key: 'home',     label: '首页',   href: 'home.html',     ic: 'home' },
      { key: 'category', label: '分类',   href: 'category.html', ic: 'grid' },
      { key: 'cart',     label: '购物车', href: 'cart.html',     ic: 'cart' },
      { key: 'mine',     label: '我的',   href: 'mine.html',     ic: 'user' }
    ];
    return '<nav class="tabbar" aria-label="主导航">' + tabs.map(function (t) {
      var badge = (t.key === 'cart' && cartCount() > 0) ? '<span class="badge" id="cart-badge">' + cartCount() + '</span>' : '';
      return '<a href="' + t.href + '"' + (t.key === active ? ' class="on" aria-current="page"' : '') + '>' + icon(t.ic, 24) + '<span>' + t.label + '</span>' + badge + '</a>';
    }).join('') + '</nav>';
  }
  /* 在 <div id="chrome-top"></div> / <div id="chrome-tab"></div> 占位处挂载 */
  function chrome(opts) {
    var top = document.getElementById('chrome-top');
    if (top) top.innerHTML = statusBar() + nav(opts);
    if (opts.tab) {
      var tab = document.getElementById('chrome-tab');
      if (tab) tab.innerHTML = tabBar(opts.tab);
    }
  }

  /* ── toast ── */
  var toastTimer = null;
  function toast(msg) {
    var el = document.querySelector('.toast');
    if (!el) {
      el = document.createElement('div');
      el.className = 'toast';
      document.querySelector('.screen').appendChild(el);
    }
    el.textContent = msg;
    el.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.remove('show'); }, 1600);
  }

  /* ── 商品卡渲染（首页/分类/搜索共用） ── */
  function productCard(p) {
    var soldout = p.stock === 0;
    return '<a class="product-card' + (soldout ? ' soldout' : '') + '" href="product-detail.html?id=' + p.id + '" data-od-id="card-' + p.id + '">' +
      '<div class="ph-img">' + icon(p.icon, 40) + '<span class="ph-tag">' + p.icon + '.jpg</span>' + (soldout ? '<span class="soldout-flag">已兑完</span>' : '') + '</div>' +
      '<div class="pc-info"><div class="pc-name">' + p.name + '</div>' +
      '<div class="pc-meta"><span class="price">' + fmt(p.points) + '<span class="unit">积分</span></span>' +
      '<span class="pc-stock">' + (soldout ? '补货中' : '剩 ' + p.stock + ' 件') + '</span></div></div></a>';
  }

  return {
    PRODUCTS: PRODUCTS, CATS: CATS, USER: USER,
    STATUS_LABEL: STATUS_LABEL,
    getBalance: getBalance, setBalance: setBalance, getLedger: getLedger, addLedger: addLedger,
    getCart: getCart, setCart: setCart, cartCount: cartCount,
    getOrders: getOrders, saveOrders: saveOrders,
    productById: productById, fmt: fmt,
    icon: icon, chrome: chrome, toast: toast, productCard: productCard
  };
})();
