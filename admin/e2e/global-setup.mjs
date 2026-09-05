/**
 * Playwright globalSetup: deterministic seed before every run.
 *
 * The PG container is test-only, so the reset is destructive and mirrors
 * backend/tests/e2e ResetDB (audit triggers are disabled around the DELETE
 * because audit_logs is append-only). After the reset:
 *
 *   1. viewer role (product:read + order:read + admin:self) via SQL
 *   2. superadmin + viewer administrators via the real admin-bootstrap binary
 *   3. products + one paid buyer order via the real HTTP APIs
 *
 * Everything the specs need afterwards (credentials, ids, buyer token) lands
 * in e2e/.build/seed-state.json.
 */

import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import pg from 'pg';
import { fileURLToPath } from 'node:url';

import { adminLogin, adminRequest, buyerLogin, buyerRequest } from './lib/api.mjs';
import {
  BACKEND_PORT,
  DATABASE_URL,
  PG_PORT,
  SUPER_PASSWORD,
  SUPER_USERNAME,
  VIEWER_PASSWORD,
  VIEWER_USERNAME,
} from './lib/env.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));

async function waitForBackend() {
  const deadline = Date.now() + 3 * 60 * 1000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`http://127.0.0.1:${BACKEND_PORT}/health/ready`);
      if (response.ok) return;
    } catch {
      // not up yet
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw new Error(`backend did not become ready on 127.0.0.1:${BACKEND_PORT}`);
}

function runBootstrap(username, password, role) {
  return new Promise((resolve, reject) => {
    const binary = path.join(here, '.build', 'bootstrap.exe');
    const child = spawn(binary, [
      '--username', username,
      '--password', password,
      '--role', role,
      '--actor', 'admin-e2e-seed',
    ], {
      // The binary resolves migrations from "../../migrations" relative to its
      // cwd by default; run it from anywhere with the absolute override.
      env: {
        ...process.env,
        DATABASE_URL,
        SHOP_MALL_MIGRATIONS_DIR: path.resolve(here, '..', '..', 'backend', 'migrations'),
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stderr = '';
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.on('error', reject);
    child.on('exit', (code) => {
      if (code === 0) resolve();
      else reject(new Error(`admin-bootstrap ${username} exited ${code}: ${stderr.trim()}`));
    });
  });
}

// Same destructive reset the backend e2e harness performs between tests.
const RESET_STATEMENTS = [
  `ALTER TABLE audit_logs DISABLE TRIGGER audit_logs_append_only`,
  `ALTER TABLE audit_logs DISABLE TRIGGER audit_logs_append_only_truncate`,
  `DELETE FROM order_items`,
  `DELETE FROM points_ledger`,
  `DELETE FROM orders`,
  `DELETE FROM cart_items`,
  `DELETE FROM user_addresses`,
  `DELETE FROM products`,
  `DELETE FROM users`,
  `DELETE FROM audit_logs`,
  `DELETE FROM admin_users`,
  `ALTER TABLE audit_logs ENABLE TRIGGER audit_logs_append_only_truncate`,
  `ALTER TABLE audit_logs ENABLE TRIGGER audit_logs_append_only`,
];

export default async function globalSetup() {
  await waitForBackend();

  const client = new pg.Client({ connectionString: `postgres://test:test@127.0.0.1:${PG_PORT}/shop_mall_test` });
  await client.connect();
  try {
    for (const statement of RESET_STATEMENTS) await client.query(statement);

    // viewer role: product:read + order:read only — drives menu/button
    // permission assertions (no order:ship, no write codes).
    await client.query(
      `INSERT INTO roles (name, remark) VALUES ('viewer', 'E2E read-only viewer') ON CONFLICT (name) DO NOTHING`,
    );
    await client.query(`DELETE FROM role_permissions WHERE role_id = (SELECT id FROM roles WHERE name = 'viewer')`);
    await client.query(`
      INSERT INTO role_permissions (role_id, permission_id)
      SELECT r.id, p.id FROM roles r JOIN permissions p
      ON p.code IN ('product:read', 'order:read', 'admin:self')
      WHERE r.name = 'viewer'
    `);
  } finally {
    await client.end();
  }

  await runBootstrap(SUPER_USERNAME, SUPER_PASSWORD, 'super_admin');
  await runBootstrap(VIEWER_USERNAME, VIEWER_PASSWORD, 'viewer');

  const session = await adminLogin(SUPER_USERNAME, SUPER_PASSWORD);

  const productA = await adminRequest(session, 'POST', '/api/admin/v1/products', {
    name: 'E2E 在售商品A',
    description: '切片一：用于列表与上下架闭环',
    category: 'digital',
    price_points: '100',
    stock: 50,
    status: 'on_sale',
  });
  const productB = await adminRequest(session, 'POST', '/api/admin/v1/products', {
    name: 'E2E 下单商品B',
    description: '切片一：买家下单用，单价最低',
    category: 'food',
    price_points: '5',
    stock: 30,
    status: 'on_sale',
  });
  const productC = await adminRequest(session, 'POST', '/api/admin/v1/products', {
    name: 'E2E 下架商品C',
    description: '切片一：种子数据即下架',
    category: 'home',
    price_points: '200',
    stock: 10,
    status: 'off_sale',
  });
  if (!productA?.id || !productB?.id || !productC?.id) {
    throw new Error('product seeding did not return ids');
  }

  // Buyer: fake WeChat client maps any code to one fixed openid, so the buyer
  // from a previous run is the same user; after the reset this is a fresh
  // signup carrying the 1000-point bonus.
  const login = await buyerLogin('admin-e2e-buyer');
  const buyerToken = login.access_token;

  const address = await buyerRequest(buyerToken, 'POST', '/api/v1/addresses', {
    receiver: 'E2E收货人',
    phone: '13800138000',
    region: '上海市 上海市 浦东新区',
    detail: '世纪大道100号',
    is_default: true,
  });
  const cartItem = await buyerRequest(buyerToken, 'POST', '/api/v1/cart', {
    product_id: String(productB.id),
    quantity: 2,
  });
  const order = await buyerRequest(
    buyerToken,
    'POST',
    '/api/v1/orders',
    { cart_item_ids: [String(cartItem.id)], address_id: String(address.id) },
    { 'Idempotency-Key': randomUUID() },
  );
  if (order?.status !== 'paid') {
    throw new Error(`seed order status is ${order?.status}, expected paid`);
  }

  const state = {
    superAdmin: { username: SUPER_USERNAME, password: SUPER_PASSWORD },
    viewer: { username: VIEWER_USERNAME, password: VIEWER_PASSWORD },
    buyerToken,
    products: {
      onSale: { id: String(productA.id), name: productA.name },
      orderable: { id: String(productB.id), name: productB.name },
      offSale: { id: String(productC.id), name: productC.name },
    },
    order: { id: String(order.id), orderNo: order.order_no },
  };
  mkdirSync(path.join(here, '.build'), { recursive: true });
  writeFileSync(path.join(here, '.build', 'seed-state.json'), JSON.stringify(state, null, 2));
  console.error(`[e2e seed] ready: order ${order.order_no} paid, products A/B/C seeded`);
}
