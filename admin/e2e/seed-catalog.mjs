/**
 * Catalog seed: enriches the验收 environment with a full product catalog.
 *
 * Idempotent — products are matched by name and skipped when already present,
 * and downloaded images are cached under .build/catalog-images/ so re-runs do
 * not hit the network again. Images are real product photos from Unsplash
 * (free-to-use license) at a fixed width; each upload goes through the real
 * POST /api/admin/v1/images endpoint so audit rows exist.
 *
 * Run AFTER global-setup (which resets products):
 *   ADMIN_E2E_BACKEND_PORT=8080 node admin/e2e/seed-catalog.mjs
 */

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { adminLogin, adminRequest } from './lib/api.mjs';
import { APP_URL, BACKEND_URL, SUPER_USERNAME, SUPER_PASSWORD } from './lib/env.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const buildDir = path.join(here, '.build');
const cacheDir = path.join(buildDir, 'catalog-images');

// slug -> { name, description, category, price_points, stock, imageUrl }
const CATALOG = [
  // 数码
  { slug: 'earbuds', name: '降噪无线耳机', description: '主动降噪，续航 30 小时，Type-C 快充，通勤听歌都够用', category: 'digital', price_points: '8900', stock: 35, imageUrl: 'https://images.unsplash.com/photo-1505740420928-5e560c06d30e?w=600&q=75' },
  { slug: 'smart-watch', name: '智能运动手表', description: '心率血氧监测，5ATM 防水，正常使用续航 14 天', category: 'digital', price_points: '32000', stock: 20, imageUrl: 'https://images.unsplash.com/photo-1546868871-7041f2a55e12?w=600&q=75' },
  { slug: 'mech-keyboard', name: '复古机械键盘', description: '87 键茶轴，PBT 键帽，有线蓝牙双模，打字手感扎实', category: 'digital', price_points: '6800', stock: 40, imageUrl: 'https://images.unsplash.com/photo-1587829741301-dc798b83add3?w=600&q=75' },
  { slug: 'bluetooth-speaker', name: '便携蓝牙音箱', description: 'IPX7 防水，12 小时续航，两只可串联成环绕声', category: 'digital', price_points: '12000', stock: 25, imageUrl: 'https://images.unsplash.com/photo-1608043152269-423dbba4e7e1?w=600&q=75' },
  { slug: 'film-camera', name: '复古胶片相机', description: '全手动操作，机械快门，入门胶片摄影的第一台', category: 'digital', price_points: '45000', stock: 8, imageUrl: 'https://images.unsplash.com/photo-1526170375885-4d8ecf77b99f?w=600&q=75' },
  // 家居
  { slug: 'wood-lamp', name: '北欧原木台灯', description: '暖光无频闪，实木底座，三档调光，书房卧室都合适', category: 'home', price_points: '9600', stock: 18, imageUrl: 'https://images.unsplash.com/photo-1507473885765-e6ed057f782c?w=600&q=75' },
  { slug: 'bean-bag', name: '云朵懒人沙发', description: '高回弹海绵，外套可拆洗，单人位放在客厅刚好', category: 'home', price_points: '26000', stock: 10, imageUrl: 'https://images.unsplash.com/photo-1586023492125-27b2c045efd7?w=600&q=75' },
  { slug: 'candle-gift', name: '香薰蜡烛礼盒', description: '大豆蜡，三种香型，单支燃烧约 40 小时，送礼自用都行', category: 'home', price_points: '1800', stock: 50, imageUrl: 'https://images.unsplash.com/photo-1602874801007-bd458bb1b8b6?w=600&q=75' },
  // 美妆
  { slug: 'lipstick', name: '丝绒哑光口红', description: '雾面质地，显色持久，上嘴不拔干，日常色号好搭', category: 'beauty', price_points: '3200', stock: 60, imageUrl: 'https://images.unsplash.com/photo-1596462502278-27bfdc403348?w=600&q=75' },
  { slug: 'perfume', name: '淡香水 50ml', description: '前调柑橘，中调茉莉，后调雪松，清新不冲', category: 'beauty', price_points: '4800', stock: 30, imageUrl: 'https://images.unsplash.com/photo-1585386959984-a4155224a1ad?w=600&q=75' },
  { slug: 'moisturizer', name: '玻尿酸保湿面霜', description: '敏感肌可用，锁水约 24 小时，早晚各一次', category: 'beauty', price_points: '2600', stock: 45, imageUrl: 'https://images.unsplash.com/photo-1571781926291-c477ebfd024b?w=600&q=75' },
  // 食品
  { slug: 'coffee-beans', name: '精品手冲咖啡豆', description: '埃塞俄比亚耶加雪菲，中度烘焙，250g 装，果酸明亮', category: 'food', price_points: '3500', stock: 40, imageUrl: 'https://images.unsplash.com/photo-1447933601403-0c6688de566e?w=600&q=75' },
  { slug: 'nut-giftbox', name: '每日坚果礼盒', description: '30 袋独立装，六种坚果混合，开袋即食', category: 'food', price_points: '2000', stock: 80, imageUrl: 'https://images.unsplash.com/photo-1599599810769-bcde5a160d32?w=600&q=75' },
  { slug: 'cookies', name: '手工黄油曲奇', description: '铁盒装，无添加，保质期 90 天，配咖啡正好', category: 'food', price_points: '1500', stock: 70, imageUrl: 'https://images.unsplash.com/photo-1567620905732-2d1ec7ab7445?w=600&q=75' },
  // 服饰
  { slug: 'canvas-tote', name: '帆布托特包', description: '加厚帆布，内袋分隔，装得下 14 寸笔记本', category: 'apparel', price_points: '3800', stock: 30, imageUrl: 'https://images.unsplash.com/photo-1548036328-c9fa89d128fa?w=600&q=75' },
  { slug: 'cotton-tee', name: '纯棉基础 T 恤', description: '220g 重磅棉，宽松版型，黑白色系百搭', category: 'apparel', price_points: '1200', stock: 100, imageUrl: 'https://images.unsplash.com/photo-1576566588028-4147f3842f27?w=600&q=75' },
];

async function fetchImage(item) {
  const cachePath = path.join(cacheDir, `${item.slug}.jpg`);
  try {
    return readFileSync(cachePath);
  } catch {
    const response = await fetch(item.imageUrl, { headers: { 'User-Agent': 'shop-mall-catalog-seed/1.0' } });
    if (!response.ok) throw new Error(`download ${item.imageUrl}: ${response.status}`);
    const buffer = Buffer.from(await response.arrayBuffer());
    writeFileSync(cachePath, buffer);
    return buffer;
  }
}

async function uploadImage(session, buffer, item) {
  const form = new FormData();
  form.append('file', new Blob([buffer], { type: 'image/jpeg' }), `${item.slug}.jpg`);
  const response = await fetch(`${BACKEND_URL}/api/admin/v1/images`, {
    method: 'POST',
    headers: { Origin: APP_URL, Cookie: session.cookie, 'X-CSRF-Token': session.csrf },
    body: form,
  });
  const body = await response.json().catch(() => null);
  if (!response.ok || !body?.data?.key) {
    throw new Error(`upload ${item.slug}: ${response.status} ${body?.message ?? '<no message>'}`);
  }
  return body.data.key;
}

async function main() {
  mkdirSync(cacheDir, { recursive: true });
  const session = await adminLogin(SUPER_USERNAME, SUPER_PASSWORD);

  // Existing names -> skip, so re-runs are safe.
  const existing = await adminRequest(session, 'GET', '/api/admin/v1/products?page=1&page_size=100&status=on_sale');
  const names = new Set((existing?.list ?? []).map((p) => p.name));

  let created = 0;
  let skipped = 0;
  let failed = 0;
  for (const item of CATALOG) {
    if (names.has(item.name)) {
      console.log(`[skip] ${item.name} 已存在`);
      skipped += 1;
      continue;
    }
    try {
      const buffer = await fetchImage(item);
      const key = await uploadImage(session, buffer, item);
      const product = await adminRequest(session, 'POST', '/api/admin/v1/products', {
        name: item.name,
        description: item.description,
        category: item.category,
        price_points: item.price_points,
        stock: item.stock,
        status: 'on_sale',
        images: [key],
      });
      if (!product?.id) throw new Error('create product returned no id');
      console.log(`[ok]   ${item.name}（${item.category}，${item.price_points} 积分，${item.stock} 件）`);
      created += 1;
    } catch (err) {
      console.error(`[fail] ${item.name}: ${err.message}`);
      failed += 1;
    }
  }
  console.log(`\n完成：新建 ${created}，跳过 ${skipped}，失败 ${failed}`);
  if (failed > 0) process.exitCode = 1;
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
