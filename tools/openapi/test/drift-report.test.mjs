import assert from "node:assert/strict";
import test from "node:test";

import { buildStaleErrorMessage } from "../drift-report.mjs";

const checkMiniapp = [
  "export interface Product {",
  "  description?: string;",
  "  category: components['schemas']['CategoryKey'];",
  "}",
].join("\n");
const regeneratedMiniapp = [
  "export interface Product {",
  "  description?: string;",
  "  subtitle?: string;",
  "  category: components['schemas']['CategoryKey'];",
  "}",
].join("\n");
const checkedInAdmin = ["export interface Ping { ok: string; }"].join("\n");
const regeneratedAdmin = ["export interface Ping { ok: string; }", "", "/* trailing */"].join("\n");

test("stale error names every drifted file with repo-relative paths", () => {
  const message = buildStaleErrorMessage([
    { file: "miniapp/src/services/generated/api.d.ts", checkedIn: checkMiniapp, regenerated: regeneratedMiniapp },
    { file: "admin/src/services/generated/api.d.ts", checkedIn: checkedInAdmin, regenerated: regeneratedAdmin },
  ]);

  assert.match(message, /contract drift detected \(2 stale files\)/);
  assert.match(message, /miniapp\/src\/services\/generated\/api\.d\.ts/);
  assert.match(message, /admin\/src\/services\/generated\/api\.d\.ts/);
});

test("stale error shows the exact drifted lines instead of bare 'stale'", () => {
  const message = buildStaleErrorMessage([
    { file: "miniapp/src/services/generated/api.d.ts", checkedIn: checkMiniapp, regenerated: regeneratedMiniapp },
  ]);

  assert.match(message, /^\+ {2}subtitle\?: string;$/m);
  assert.ok(message.includes("checked in") && message.includes("regenerated"));
  assert.doesNotMatch(message, /at <anonymous>|at async /, "no Node stack noise in the report body");
});

test("stale error ends with the fix command", () => {
  const message = buildStaleErrorMessage([
    { file: "admin/src/services/generated/api.d.ts", checkedIn: checkedInAdmin, regenerated: regeneratedAdmin },
  ]);

  assert.match(message, /pnpm --filter @shop-mall\/openapi generate/);
});

test("large drifts are truncated with an explicit marker", () => {
  const bigChecked = Array.from({ length: 500 }, (_, i) => `line-${i}`).join("\n");
  const bigRegen = Array.from({ length: 500 }, (_, i) => `other-${i}`).join("\n");
  const message = buildStaleErrorMessage([
    { file: "miniapp/src/services/generated/api.d.ts", checkedIn: bigChecked, regenerated: bigRegen },
  ], { maxDiffLines: 40 });

  const diffLines = message.split("\n").filter((line) => /^[-+]/.test(line));
  assert.ok(diffLines.length <= 40, `expected truncation to <=40 diff lines, got ${diffLines.length}`);
  assert.match(message, /truncated/);
});
