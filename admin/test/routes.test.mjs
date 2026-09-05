import assert from "node:assert/strict";
import test from "node:test";

import routes from "../config/routes.ts";
import { MENU_ITEMS } from "../src/menu.ts";

test("route table contains login plus every menu section", () => {
  const names = routes.filter((route) => route.name).map((route) => route.name);
  assert.deepEqual(
    new Set(names),
    new Set(["登录", ...MENU_ITEMS.map((item) => item.name)]),
  );
});

test("login is layout-free and outside access control", () => {
  const login = routes.find((route) => route.path === "/user/login");
  assert.ok(login);
  assert.equal(login.layout, false);
  assert.ok(!login.access);
});

test("every menu section maps to a content route with the same permission code and a component", () => {
  for (const item of MENU_ITEMS) {
    const route = routes.find((candidate) => candidate.path === item.path);
    assert.ok(route, `${item.path} has a route`);
    assert.ok(route.component, `${item.path} has a component`);
    assert.equal(
      route.access,
      item.permission,
      `${item.path} access must mirror the menu permission code`,
    );
  }
});
