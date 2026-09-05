import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import test from "node:test";
import { parse } from "yaml";

const root = resolve(import.meta.dirname, "../../..");
const contractPath = resolve(root, "docs/api/openapi.yaml");
const document = parse(readFileSync(contractPath, "utf8"));
const generatedPaths = [
  resolve(root, "miniapp/src/services/generated/api.d.ts"),
  resolve(root, "admin/src/services/generated/api.d.ts"),
];

test("contract-check validates the frozen contract and generated outputs", () => {
  // Windows: pnpm installs as pnpm.cmd, which spawnSync only resolves through
  // a shell. The args here are static constants, so the shell hop is safe.
  execFileSync("pnpm", ["--filter", "@shop-mall/openapi", "run", "contract:check"], {
    cwd: root,
    stdio: "pipe",
    ...(process.platform === "win32" ? { shell: true } : {}),
  });

  const contract = readFileSync(contractPath, "utf8");
  assert.match(contract, /TraceId:/);
  assert.match(contract, /BadRequest:/);
  assert.match(contract, /InternalServerError:/);
  assert.doesNotMatch(contract, /format: int64/);

  for (const generatedPath of generatedPaths) {
    assert.ok(existsSync(generatedPath), `missing generated output: ${generatedPath}`);
    const generated = readFileSync(generatedPath, "utf8");
    assert.match(generated, /export interface components/);
    assert.match(generated, /export type ApiTransport/);
  }
});

test("contract-check covers security, transaction, and permission boundaries", () => {
  const paths = document.paths;
  const components = document.components;
  const getOperation = (path, method) => paths[path][method];

  assert.equal(getOperation("/health/ready", "get").responses["503"].$ref, "#/components/responses/ServiceUnavailable");
  assert.equal(getOperation("/api/admin/v1/roles", "get").responses["200"].headers["X-Trace-Id"].$ref, "#/components/headers/TraceId");

  for (const operationId of ["adminLogin", "adminLogout", "changeAdminPassword"]) {
    const operation = Object.values(paths).flatMap((path) => Object.values(path)).find((candidate) => candidate.operationId === operationId);
    assert.ok(operation, `missing operation ${operationId}`);
    assert.ok(operation.responses["403"], `${operationId} must describe 403`);
  }

  const info = document.info.description;
  assert.match(info, /30-minute lifetime/);
  assert.match(info, /HttpOnly; Secure; SameSite=Strict; Path=\/api\/admin\/v1/);
  assert.match(info, /csrf_token[\s\S]*readable by the frontend/);
  assert.match(info, /X-CSRF-Token.*exactly match the csrf_token cookie/);
  assert.match(info, /Max-Age=0/);
  assert.match(info, /Missing or invalid Origin[\s\S]*return 403/);

  assert.match(components.securitySchemes.AdminCookie.description, /30-minute lifetime/);
  assert.match(components.securitySchemes.AdminCookie.description, /HttpOnly; Secure; SameSite=Strict; Path=\/api\/admin\/v1/);
  assert.match(components.securitySchemes.CsrfCookie.description, /Frontend JavaScript may read/);
  assert.match(components.securitySchemes.CsrfCookie.description, /not an identity credential/);
  assert.match(components.securitySchemes.CsrfToken.description, /exactly match the csrf_token cookie/);

  const adminWriteOperationIds = [
    "adminLogout",
    "changeAdminPassword",
    "adminCreateProduct",
    "adminUpdateProduct",
    "uploadImage",
    "shipOrder",
    "approveRefund",
    "rejectRefund",
    "adjustPoints",
    "createRole",
    "updateRole",
    "updateAdminUser",
  ];
  for (const operationId of adminWriteOperationIds) {
    const operation = Object.values(paths).flatMap((path) => Object.values(path)).find((candidate) => candidate.operationId === operationId);
    assert.ok(operation.security[0].CsrfCookie, `${operationId} must require csrf_token cookie`);
    assert.ok(operation.security[0].CsrfToken, `${operationId} must require X-CSRF-Token`);
    assert.match(operation.description, /allowed HTTPS Origin/);
    assert.match(operation.description, /exactly[\s\S]*matches the csrf_token cookie/);
    assert.match(operation.description, /return 403/);
  }

  const address = components.schemas.Address;
  assert.ok(address.required.includes("version"));
  assert.equal(address.properties.version["x-public-int64"], true);

  const order = components.schemas.Order;
  for (const field of ["shipped_at", "completed_at", "refunded_at"]) {
    assert.ok(order.required.includes(field));
    assert.deepEqual(order.properties[field].type, ["string", "null"]);
  }

  const createOrder = getOperation("/api/v1/orders", "post");
  assert.match(createOrder.description, /address ID.*address version.*server-side address snapshot/s);
  assert.match(createOrder.description, /selected cart items are deleted in the same database transaction/);

  const getProduct = getOperation("/api/v1/products/{productId}", "get");
  assert.match(getProduct.description, /off_sale.*404/);
  assert.equal(getProduct.responses["404"].$ref, "#/components/responses/NotFound");

  for (const operationId of ["createRole", "updateRole"]) {
    const operation = Object.values(paths).flatMap((path) => Object.values(path)).find((candidate) => candidate.operationId === operationId);
    assert.equal(operation.responses["422"].$ref, "#/components/responses/BusinessError");
    assert.match(operation.description, /Unknown permission codes.*422/);
  }

  assert.match(getOperation("/api/admin/v1/admin-users", "get").description, /bootstrap-only/);
  assert.equal(paths["/api/admin/v1/admin-users"].post, undefined);
  assert.equal(getOperation("/api/admin/v1/auth/logout", "post").responses["200"].$ref, "#/components/responses/AdminLogoutSuccess");
});
