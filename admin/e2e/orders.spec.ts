/**
 * ADMIN_TARGET.md 第 4 节「订单管理」的 5 条断言。
 *
 * 执行顺序有依赖：O4（viewer 无发货按钮）必须在 O3（发货）之前跑——
 * O3 之后种子订单不再是 paid，发货按钮本就不会出现，O4 会失去判别力。
 * 单 worker 顺序执行（见 playwright.config.ts）保证了这一点。
 */

import { expect, test } from './fixtures';

const FIVE_STATES = ['待发货', '已发货', '已完成', '退款申请中', '已退款'];

async function openSeedOrderDetail(page: import('@playwright/test').Page, orderNo: string) {
  await page.goto('/#/orders');
  const row = page.getByRole('row', { name: new RegExp(orderNo) });
  await expect(row).toBeVisible();
  await row.getByText('详情').click();
  const modal = page.locator('.ant-modal:visible', { hasText: orderNo }).first();
  await expect(modal).toBeVisible();
  return modal;
}

test('O1 状态筛选传后端并生效，五种状态齐全', async ({ adminPage, seed }) => {
  await adminPage.goto('/#/orders');

  // 查询表单默认收起，先展开再校验五种状态齐全。
  await adminPage.getByText('展开').first().click();
  const statusSelect = adminPage.locator('.ant-pro-table-search .ant-select').first();
  const dropdown = adminPage.locator('.ant-select-dropdown:visible');
  await statusSelect.click();
  for (const state of FIVE_STATES) {
    await expect(
      dropdown.locator('.ant-select-item-option', { hasText: state }),
    ).toBeVisible();
  }
  await adminPage.keyboard.press('Escape');

  // 待发货筛选：请求带 status=paid，种子订单在结果里。
  await statusSelect.click();
  await dropdown.locator('.ant-select-item-option', { hasText: '待发货' }).click();
  const paidRequest = adminPage.waitForRequest(
    (request) =>
      request.url().includes('/api/admin/v1/orders') && request.url().includes('status=paid'),
  );
  await adminPage.getByRole('button', { name: '查 询' }).click();
  await paidRequest;
  await expect(
    adminPage.getByRole('row', { name: new RegExp(seed.order.orderNo) }),
  ).toBeVisible();

  // 已发货筛选：种子订单还是 paid，不应出现。
  await statusSelect.click();
  await dropdown.locator('.ant-select-item-option', { hasText: '已发货' }).click();
  const shippedRequest = adminPage.waitForRequest(
    (request) =>
      request.url().includes('/api/admin/v1/orders') && request.url().includes('status=shipped'),
  );
  await adminPage.getByRole('button', { name: '查 询' }).click();
  await shippedRequest;
  await expect(
    adminPage.getByRole('row', { name: new RegExp(seed.order.orderNo) }),
  ).toHaveCount(0);
});

test('O2 详情逐项渲染收货人、地址、电话与商品明细', async ({ adminPage, seed }) => {
  const modal = await openSeedOrderDetail(adminPage, seed.order.orderNo);
  await expect(modal).toContainText('待发货');
  await expect(modal).toContainText('E2E收货人');
  await expect(modal).toContainText('13800138000');
  await expect(modal).toContainText('世纪大道100号');
  await expect(modal).toContainText(seed.products.orderable.name);
  await expect(modal.getByText('已发货').first()).not.toBeVisible();
  // 明细行：单价 5 × 数量 2，小计 10，订单总额 10。
  await expect(modal.getByRole('row', { name: new RegExp(seed.products.orderable.name) })).toContainText('10');
});

test('O4 viewer 无 order:ship 权限时发货按钮不渲染', async ({ viewerPage, seed }) => {
  const modal = await openSeedOrderDetail(viewerPage, seed.order.orderNo);
  await expect(modal).toContainText('待发货');
  await expect(modal.getByRole('button', { name: '发货' })).toHaveCount(0);
});

test('O3 超管对 paid 订单发货，状态变为 shipped', async ({ adminPage, seed }) => {
  const modal = await openSeedOrderDetail(adminPage, seed.order.orderNo);
  // antd 两字按钮自动加空格："发 货"。
  await modal.getByRole('button', { name: /发\s*货/ }).click();
  await adminPage.getByRole('button', { name: '确 定' }).click();
  await expect(adminPage.getByText('订单已发货')).toBeVisible();

  // 详情内状态即时更新为已发货。
  await expect(modal.getByText('已发货').first()).toBeVisible();
});

test('O5 发货后买家端订单状态变 shipped（数据闭环）', async ({ seed, request }) => {
  const response = await request.get(`/api/v1/orders/${seed.order.id}`, {
    headers: { Authorization: `Bearer ${seed.buyerToken}` },
  });
  expect(response.ok()).toBe(true);
  const body = await response.json();
  expect(body?.data?.status).toBe('shipped');
});
