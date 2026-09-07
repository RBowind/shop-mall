// 契约漂移报告：把"generated output is stale"升级成能直接读出漂移内容的红灯。
// 纯函数 + 多集行 diff（无依赖、对确定性生成产物足够），供 generate.ts 与单测共用。

function countLines(text) {
  const counts = new Map();
  for (const line of text.split("\n")) counts.set(line, (counts.get(line) ?? 0) + 1);
  return counts;
}

// 多集差：added = regenerated 中超出 checkedIn 计数的行（按 regenerated 顺序），
// removed = checkedIn 中超出 regenerated 计数的行（按 checkedIn 顺序）。
function multisetDiff(checkedIn, regenerated) {
  const inChecked = countLines(checkedIn);
  const inRegen = countLines(regenerated);
  const removed = [];
  const added = [];
  const seenChecked = new Map();
  const seenRegen = new Map();
  for (const line of regenerated.split("\n")) {
    const seen = seenRegen.get(line) ?? 0;
    if (seen >= (inChecked.get(line) ?? 0)) added.push(`+${line}`);
    seenRegen.set(line, seen + 1);
  }
  for (const line of checkedIn.split("\n")) {
    const seen = seenChecked.get(line) ?? 0;
    if (seen >= (inRegen.get(line) ?? 0)) removed.push(`-${line}`);
    seenChecked.set(line, seen + 1);
  }
  return { removed, added };
}

export function buildStaleErrorMessage(entries, { maxDiffLines = 40 } = {}) {
  const lines = [
    `contract drift detected (${entries.length} stale ${entries.length === 1 ? "file" : "files"}):`,
    "the checked in TypeScript types no longer match what docs/api/openapi.yaml regenerates.",
    "diff legend: '-' = checked in (gone after regenerate), '+' = regenerated (missing from checked in)",
    "",
  ];
  let budget = maxDiffLines;
  for (const { file, checkedIn, regenerated } of entries) {
    lines.push(`file: ${file}`);
    const { removed, added } = multisetDiff(checkedIn, regenerated);
    if (removed.length + added.length === 0) {
      lines.push("  (byte-level difference without line-level diff; rerun generate to inspect)");
    }
    const shown = [...removed, ...added].slice(0, budget);
    lines.push(...shown);
    budget -= shown.length;
    if (budget <= 0) {
      lines.push("… diff truncated — run generate locally to see the full output");
      break;
    }
    lines.push("");
  }
  lines.push("fix: pnpm --filter @shop-mall/openapi generate  (then commit the regenerated api.d.ts files)");
  return lines.join("\n");
}
