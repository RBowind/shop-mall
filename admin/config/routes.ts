/**
 * Umi route table.
 *
 * The content sections are derived from `src/menu.ts` (the single permission
 * mapping). Every content route carries `access` = its permission code, which
 * the plugin-access factory (`src/access.ts` default export) resolves: a route
 * whose permission the session lacks is hidden from the menu and renders an
 * access-denied view when addressed directly. `login` opts out of access
 * control and of the ProLayout shell (`layout: false`).
 */

import { MENU_ITEMS } from '../src/menu.ts';

const loginRoute = {
  path: '/user/login',
  layout: false,
  component: './user/login',
  name: '登录',
};

const contentRoutes = MENU_ITEMS.map((item) => ({
  path: item.path,
  component: `./${item.path.slice(1)}`,
  name: item.name,
  access: item.permission,
}));

const routes = [
  { path: '/', redirect: '/products' },
  loginRoute,
  ...contentRoutes,
];

export default routes;