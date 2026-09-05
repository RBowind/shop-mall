/**
 * ADMIN_TARGET.md 第 3 节「商品管理」的 6 条断言。
 *
 * 数据闭环（P3/P6）通过公开的买家端商品列表接口直接验证。
 * ProTable 查询表单默认收起多余字段，操作筛选前先点「展开」。
 */

import { expect, test } from './fixtures';

/** 买家端公开商品列表（无需登录），返回信封里的 list。 */
async function buyerProducts(request: import('@playwright/test').APIRequestContext, name: string) {
  const response = await request.get('/api/v1/products', {
    params: { page: 1, page_size: 100 },
  });
  expect(response.ok()).toBe(true);
  const body = await response.json();
  const list: Array<{ id: string; name: string; status: string }> = body?.data?.list ?? [];
  return list.find((item) => item.name === name);
}

/** 展开收起的查询字段，返回状态筛选下拉。 */
async function openStatusFilter(page: import('@playwright/test').Page) {
  await page.getByText('展开').first().click();
  return page.locator('.ant-pro-table-search .ant-select').first();
}

test('P1 列表分页渲染状态、价格、库存', async ({ adminPage, seed }) => {
  await adminPage.goto('/#/products');
  const row = adminPage.getByRole('row', { name: new RegExp(seed.products.onSale.name) });
  await expect(row).toBeVisible();
  await expect(row).toContainText('在售');
  await expect(row).toContainText('100');
  await expect(row).toContainText('50');
});

test('P2 状态筛选把 status 真实传给后端且结果一致', async ({ adminPage, seed }) => {
  await adminPage.goto('/#/products');

  const statusSelect = await openStatusFilter(adminPage);
  // antd 保留已关闭下拉的隐藏 DOM（内部 id 重复），选项必须限定在可见
  // 面板内，且按 antd 的选项类名定位。
  const dropdown = adminPage.locator('.ant-select-dropdown:visible');
  await statusSelect.click();
  await dropdown.locator('.ant-select-item-option', { hasText: '在售' }).click();
  const onSaleRequest = adminPage.waitForRequest(
    (request) =>
      request.url().includes('/api/admin/v1/products') &&
      request.url().includes('status=on_sale'),
  );
  await adminPage.getByRole('button', { name: '查 询' }).click();
  await onSaleRequest;
  await expect(adminPage.getByRole('row', { name: new RegExp(seed.products.onSale.name) })).toBeVisible();
  await expect(adminPage.getByRole('row', { name: new RegExp(seed.products.offSale.name) })).toHaveCount(0);

  await statusSelect.click();
  await dropdown.locator('.ant-select-item-option', { hasText: '下架' }).click();
  const offSaleRequest = adminPage.waitForRequest(
    (request) =>
      request.url().includes('/api/admin/v1/products') &&
      request.url().includes('status=off_sale'),
  );
  await adminPage.getByRole('button', { name: '查 询' }).click();
  await offSaleRequest;
  await expect(adminPage.getByRole('row', { name: new RegExp(seed.products.offSale.name) })).toBeVisible();
  await expect(adminPage.getByRole('row', { name: new RegExp(seed.products.onSale.name) })).toHaveCount(0);
});

test('P3 新建商品后买家端能查到（数据闭环）', async ({ adminPage }) => {
  const name = `E2E 闭环新商品P3-${Date.now()}`;
  await adminPage.goto('/#/products');
  await adminPage.getByRole('button', { name: '新建商品' }).click();
  // 弹窗表单与搜索表单字段同名（DOM id 重复导致 getByLabel 解析到
  // 搜索框），按表单项容器定位弹窗内字段。
  const modal = adminPage.locator('.ant-modal:visible');
  const field = (label: string) =>
    modal.locator('.ant-form-item').filter({ hasText: label }).locator('input');
  await field('名称').fill(name);
  await field('价格（积分）').fill('66');
  await field('库存').fill('8');
  await modal.getByRole('button', { name: '保 存' }).click();
  await expect(adminPage.getByText('商品已创建')).toBeVisible();

  const found = await buyerProducts(adminPage.context().request, name);
  expect(found).toBeTruthy();
  expect(found?.status).toBe('on_sale');
});

test('P4 编辑商品后详情接口返回新值', async ({ adminPage, seed }) => {
  await adminPage.goto('/#/products');
  const row = adminPage.getByRole('row', { name: new RegExp(seed.products.onSale.name) });
  await row.getByRole('link', { name: '编辑' }).click();

  const modal = adminPage.locator('.ant-modal:visible');
  const field = (label: string) =>
    modal.locator('.ant-form-item').filter({ hasText: label }).locator('input');
  await field('价格（积分）').fill('123');
  await field('库存').fill('45');
  await modal.getByRole('button', { name: '保 存' }).click();
  await expect(adminPage.getByText('商品已更新')).toBeVisible();

  const response = await adminPage.context().request.get(
    `/api/admin/v1/products/${seed.products.onSale.id}`,
  );
  expect(response.ok()).toBe(true);
  const body = await response.json();
  expect(String(body?.data?.price_points)).toBe('123');
  expect(String(body?.data?.stock)).toBe('45');
});

test('P5 多图上传：回填两张、首张主图标记、超限与非法格式被拒', async ({ adminPage }) => {
  const { ensureImageFixtures } = await import('./lib/images.mjs');
  const { tiny, oversize, invalid, hugePixels } = ensureImageFixtures();

  await adminPage.goto('/#/products');
  await adminPage.getByRole('button', { name: '新建商品' }).click();
  const modal = adminPage.locator('.ant-modal:visible');
  const field = (label: string) =>
    modal.locator('.ant-form-item').filter({ hasText: label }).locator('input');
  const fileInput = modal.locator('input[type="file"]');
  const galleryImgs = () => modal.locator('img[src*="/static/images/"]');

  // 两张合法图（同文件两次上传，服务端各生成独立 key）。
  await fileInput.setInputFiles(tiny);
  await expect(adminPage.getByText(/图片已上传/).first()).toBeVisible();
  await fileInput.setInputFiles(tiny);
  await expect(adminPage.getByText(/图片已上传/).first()).toBeVisible();
  await expect(galleryImgs()).toHaveCount(2);
  // 只有第一张带主图标记。
  await expect(modal.locator('[aria-label="主图"]')).toHaveCount(1);

  // 超 2MB：浏览器端自动压缩后上传成功（不再是硬拒绝）。
  await fileInput.setInputFiles(oversize);
  await expect(adminPage.getByText(/已压缩至/)).toBeVisible();
  await expect(adminPage.getByText(/图片已上传/).first()).toBeVisible();
  await expect(galleryImgs()).toHaveCount(3);

  // 小体积但超大像素（30MP > 后端 25M 像素上限）：压缩管线必须连像素
  // 一起压，而不是只看文件体积放行。
  await fileInput.setInputFiles(hugePixels);
  await expect(adminPage.getByText(/已压缩至/)).toBeVisible();
  await expect(galleryImgs()).toHaveCount(4);

  // 非 JPEG/PNG/WebP：类型拦截提示（压缩不背锅，格式仍然拒绝）。
  await fileInput.setInputFiles(invalid);
  await expect(adminPage.getByText('仅支持 JPEG / PNG / WebP 图片')).toBeVisible();

  // 缩略图必须真实加载（像素 > 0），不只是渲染了 img 标签——防 URL
  // 指向不可达源的裂图回归。
  await expect(async () => {
    const loaded = await galleryImgs().first().evaluate((el) => el.naturalWidth > 0);
    expect(loaded).toBe(true);
  }).toPass();

  // 保存后买家端 images 数组四张，main_image 即第一张（数据闭环）。
  const name = `E2E 多图商品P5-${Date.now()}`;
  await field('名称').fill(name);
  await field('价格（积分）').fill('30');
  await field('库存').fill('6');
  await modal.getByRole('button', { name: '保 存' }).click();
  await expect(adminPage.getByText('商品已创建')).toBeVisible();

  const response = await adminPage.context().request.get('/api/v1/products', {
    params: { page: 1, page_size: 100 },
  });
  const body = await response.json();
  const found = (body?.data?.list ?? []).find((item: { name: string }) => item.name === name);
  expect(found?.images?.length).toBe(4);
  expect(found?.main_image).toBe(found?.images?.[0]);
});

test('P6 下架后买家端不可见，上架后恢复', async ({ adminPage, seed }) => {
  const request0 = adminPage.context().request;
  await adminPage.goto('/#/products');
  const row = adminPage.getByRole('row', { name: new RegExp(seed.products.onSale.name) });
  await expect(row).toBeVisible();

  // 下架。
  await row.getByRole('link', { name: '下架' }).click();
  await adminPage.getByRole('button', { name: '确 定' }).click();
  await expect(adminPage.getByText('商品已下架')).toBeVisible();

  // 买家端在售列表不再展示该商品（下架商品不出现在公开列表里）。
  const response = await request0.get('/api/v1/products', {
    params: { page: 1, page_size: 100 },
  });
  const body = await response.json();
  const names = (body?.data?.list ?? []).map((item: { name: string }) => item.name);
  expect(names).not.toContain(seed.products.onSale.name);

  // 上架恢复。
  const rowAfter = adminPage.getByRole('row', { name: new RegExp(seed.products.onSale.name) });
  await rowAfter.getByRole('link', { name: '上架' }).click();
  await adminPage.getByRole('button', { name: '确 定' }).click();
  await expect(adminPage.getByText('商品已上架')).toBeVisible();
  const restored = await buyerProducts(request0, seed.products.onSale.name);
  expect(restored?.status).toBe('on_sale');
});
