/**
 * ADMIN_TARGET.md 第 1 节「全局壳与权限」的 4 条断言。
 *
 * S1 未登录重定向；S2 菜单按权限渲染 + 直访无权限路由被拦；
 * S3 写请求自动注入 X-CSRF-Token（登录接口除外）；
 * S4 会话失效统一跳回登录页并提示。
 */

import { expect, test } from './fixtures';

test('S1 未登录访问受保护路由重定向到登录页', async ({ page }) => {
  await page.goto('/#/products');
  await expect(page).toHaveURL(/#\/user\/login/);
});

test('S2 viewer 菜单按权限渲染且直访无权限路由被拦', async ({ viewerPage }) => {
  await viewerPage.goto('/#/products');

  // viewer 持 product:read + order:read：只应看到这两个菜单入口。
  await expect(viewerPage.getByRole('menuitem', { name: '商品' })).toBeVisible();
  await expect(viewerPage.getByRole('menuitem', { name: '订单' })).toBeVisible();
  for (const hidden of ['退款', '积分', '权限', '会员', '审计']) {
    await expect(viewerPage.getByRole('menuitem', { name: hidden })).toHaveCount(0);
  }

  // 直访无权限 URL 不渲染积分页内容，而是无权限提示。
  await viewerPage.goto('/#/points');
  await expect(
    viewerPage.getByText(/403|无权|没有权限|无法访问|access/i).first(),
  ).toBeVisible();
});

test('S3 写请求自动注入与 csrf_token cookie 一致的 X-CSRF-Token', async ({
  adminContext,
  adminPage,
}) => {
  const cookies = await adminContext.cookies();
  const csrf = cookies.find((cookie) => cookie.name === 'csrf_token')?.value;
  expect(csrf).toBeTruthy();

  const createRequest = adminPage.waitForRequest(
    (request) =>
      request.method() === 'POST' &&
      request.url().includes('/api/admin/v1/products'),
  );

  await adminPage.goto('/#/products');
  await adminPage.getByRole('button', { name: '新建商品' }).click();
  // 弹窗表单与页面搜索表单字段同名（DOM id 重复导致 getByLabel 解析到
  // 搜索框），按表单项容器定位弹窗内字段。
  const modal = adminPage.locator('.ant-modal:visible');
  const field = (label: string) =>
    modal.locator('.ant-form-item').filter({ hasText: label }).locator('input');
  await field('名称').fill('S3 CSRF 探针商品');
  await field('价格（积分）').fill('77');
  await field('库存').fill('3');
  await modal.getByRole('button', { name: '保 存' }).click();

  const request = await createRequest;
  expect(await request.headerValue('X-CSRF-Token')).toBe(csrf);
});

test('S4 会话失效后 401 统一跳回登录页并提示', async ({ adminContext, adminPage }) => {
  await adminPage.goto('/#/products');
  await expect(adminPage.getByRole('menuitem', { name: '商品' })).toBeVisible();

  // 清掉会话 cookie 模拟过期，触发下一次 /auth/me 401。
  await adminContext.clearCookies();
  await adminPage.reload();

  await expect(adminPage).toHaveURL(/#\/user\/login\?reason=session/);
  await expect(adminPage.getByText('登录状态已失效').first()).toBeVisible();
});
