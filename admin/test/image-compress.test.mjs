import assert from "node:assert/strict";
import test from "node:test";

import { COMPRESS_TARGET_BYTES, compressionPlan } from "../src/lib/image-compress.ts";

test("compression plan tries quality first at full resolution, then scales down", () => {
  const plan = compressionPlan(true);
  // 4 resolution steps x 3 quality steps, quality-major within each scale.
  assert.equal(plan.length, 12);
  assert.deepEqual(
    plan.slice(0, 3).map((step) => step.quality),
    [0.85, 0.7, 0.55],
  );
  assert.equal(plan[0].scale, 1);
  // Resolution never increases along the plan.
  for (let i = 1; i < plan.length; i += 1) {
    assert.ok(plan[i].scale <= plan[i - 1].scale, `step ${i} increases scale`);
  }
  // Every step prefers webp.
  for (const step of plan) assert.equal(step.type, "image/webp");
});

test("compression plan falls back to jpeg when webp is unavailable", () => {
  const plan = compressionPlan(false);
  assert.ok(plan.length > 0);
  for (const step of plan) assert.equal(step.type, "image/jpeg");
});

test("compression target leaves a safety margin under the 2MB upload limit", () => {
  assert.ok(COMPRESS_TARGET_BYTES < 2 * 1024 * 1024);
  assert.ok(COMPRESS_TARGET_BYTES >= 1.5 * 1024 * 1024);
});
