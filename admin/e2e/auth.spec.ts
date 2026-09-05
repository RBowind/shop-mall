/**
 * ADMIN_TARGET.md 第 2 节「登录 / 登出」的 3 条断言（改密属切片二）。
 *
 * A1 正确/错误凭据与 cookie 写入；A2 表单校验；A3 登出后会话失效。
 */

import { expect, test } from './fixtures';

// antd 在两个中文字符的按钮文本中间自动插入一个空格（"登 录"），用正则兼容两种。
const LOGIN_BUTTON = /登\s*录/;

test('A1 正确凭据登录写入双 cookie，错误凭据 401 给中文提示', async ({ page, seed }) => {
  // 错误凭据：401 + 中文错误提示，且登录请求本身不带 CSRF header。
  await page.goto('/#/user/login');
  const loginRequest = page.waitForRequest(
    (request) =>
      request.method() === 'POST' &&
      request.url().includes('/api/admin/v1/auth/login'),
  );
  await page.getByPlaceholder('请输入用户名').fill(seed.superAdmin.username);
  await page
    .getByPlaceholder('至少 12 位，含大小写字母、数字、符号')
    .fill('Wrong#Password123');
  await page.getByRole('button', { name: LOGIN_BUTTON }).click();
  const wrongRequest = await loginRequest;
  expect(await wrongRequest.headerValue('X-CSRF-Token')).toBeNull();
  await expect(page.getByText('用户名或密码错误').first()).toBeVisible();

  // 正确凭据：换一个全新页面走完整登录流。登录页会跳过启动 me() 探测
  // （app.tsx 对 /user/login 的特判），不存在迟到 401 覆盖 session 的竞态。
  const context = page.context();
  const fresh = await context.newPage();
  await fresh.goto('/#/user/login');
  await fresh.getByPlaceholder('请输入用户名').fill(seed.superAdmin.username);
  await fresh
    .getByPlaceholder('至少 12 位，含大小写字母、数字、符号')
    .fill(seed.superAdmin.password);
  await fresh.getByRole('button', { name: LOGIN_BUTTON }).click();
  await expect(fresh).toHaveURL(/#\/products/);

  const cookies = await context.cookies();
  const names = new Set(cookies.map((cookie) => cookie.name));
  expect(names.has('admin_access_token')).toBe(true);
  expect(names.has('csrf_token')).toBe(true);
  await fresh.close();
});

test('A2 用户名与密码有非空和格式校验', async ({ page }) => {
  await page.goto('/#/user/login');

  // 空提交：两个字段都拦。
  await page.getByRole('button', { name: LOGIN_BUTTON }).click();
  await expect(page.getByText('请输入用户名')).toBeVisible();
  await expect(page.getByText('请输入密码')).toBeVisible();

  // 用户名过短（<3 位）。
  await page.getByPlaceholder('请输入用户名').fill('ab');
  await expect(page.getByText('用户名长度为 3-32 位')).toBeVisible();

  // 密码 12 位以上但缺大写和符号。
  await page
    .getByPlaceholder('至少 12 位，含大小写字母、数字、符号')
    .fill('weakpassword123');
  await expect(
    page.getByText('密码至少 12 位，且包含大写字母、小写字母、数字和符号'),
  ).toBeVisible();
});

test('A3 登出后旧会话对 /auth/me 返回 401', async ({ adminPage }) => {
  await adminPage.goto('/#/products');
  await expect(adminPage.getByRole('menuitem', { name: '商品' })).toBeVisible();

  // 头像下拉（ProLayout 把用户区放在侧边栏底部）里的退出登录。
  await adminPage.locator('.ant-dropdown-trigger').first().click();
  await adminPage.getByText('退出登录').click();

  await expect(adminPage).toHaveURL(/#\/user\/login/);
  const response = await adminPage.context().request.get('/api/admin/v1/auth/me');
  expect(response.status()).toBe(401);
});
