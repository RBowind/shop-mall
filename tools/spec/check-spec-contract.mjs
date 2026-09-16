#!/usr/bin/env node
// specs/ 下 behaviorspec 与 sprint contract 的结构一致性门禁。
//
// 为什么要这个：specs/** 不在四道门的任何正向 filter 里，又躺在 unknown 兜底的负向
// 清单里，所以只改 spec 的 PR 是四门空跑透绿——契约改了 spec 没跟、B 编号重排后测试
// 回链断掉、已勾选的 B 其实没有用例，都没有机器拦。本脚本补这一环，挂在已有的
// contract job 里（新建 job 不是 required check，得改分支保护才拦得住）。
//
// 只做机器能判死的检查。业务正确性、黑盒纯度边界、coverage 取舍留给人 gate。
//
// 判不了因而没查的：B 编号「相对上一版没有重排」需要基线 diff，本脚本无基线，跳过。

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const specsDir = join(root, "specs");
const testsDir = join(root, "backend", "tests");

const failures = [];
const warnings = [];
const note = (list, file, line, msg, fix) => list.push({ file, line, msg, fix });

const rel = (p) => relative(root, p).replace(/\\/g, "/");
const lines = (p) => readFileSync(p, "utf8").replace(/^﻿/, "").split(/\r?\n/);

function walk(dir, out = []) {
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, entry.name);
    if (entry.isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
}

const RE_REQUIREMENT = /^###\s+Requirement:\s*(.+?)\s*$/;
const RE_SCENARIO = /^####\s+Scenario:\s*(.+?)\s*$/;
const RE_SECTION = /^##\s+(?!#)(.+?)\s*$/;
const RE_B = /^- \[([ xX])\]\s+(B\d+)\s+\[(ADDED|MODIFIED|REMOVED)\]:\s*(.*)$/;
const RE_B_LOOSE = /^- \[[ xX]\]\s+B\d+/;
const RE_Q = /^- \[([ xX])\]\s+(Q\d+):\s*(.*)$/;
const RE_ANCHOR = /\/\/\s*contract:\s*(B\d+)/g;
const RE_B_REF = /\bB(\d+)\b/g;
const RE_Q_REF = /\bQ(\d+)\b/g;
// FU 有两副面孔：`- FU-xxxxxxxx: 描述` 是定义，正文里再次出现是回指。只有定义查唯一性。
const RE_FU_DEF = /^-\s*FU-([0-9a-f]{8})\b/;
const RE_FU_REF = /\bFU-([0-9a-f]{8})\b/g;

const SPEC_SECTIONS = new Set([
  "Elaborates",
  "Related Capabilities",
  "Requirements",
  "Coverage Gaps",
]);
// 契约标签形如「Requirement 名 / Scenario 名」，Scenario 名后可挂一段括号注记。
const stripNote = (s) => s.replace(/（[^（）]*）\s*$/, "").trim();

// 一个 capability 名在测试文件名里的写法：连字符与下划线互认。
const capPattern = (cap) =>
  new RegExp(`(^|/)${cap.replace(/-/g, "[_-]?")}_spec_test\\.go$`);

function parseSpec(path) {
  const src = lines(path);
  const sections = [];
  const requirements = new Map();
  let current = null;
  src.forEach((text, i) => {
    const sec = text.match(RE_SECTION);
    if (sec) {
      sections.push({ name: sec[1], line: i + 1 });
      current = null;
      return;
    }
    const req = text.match(RE_REQUIREMENT);
    if (req) {
      current = { line: i + 1, scenarios: [] };
      requirements.set(req[1], current);
      return;
    }
    const sc = text.match(RE_SCENARIO);
    if (sc && current) current.scenarios.push({ name: sc[1], line: i + 1 });
  });
  return { lines: src, sections, requirements };
}

function parseContract(path) {
  const src = lines(path);
  const out = {
    lines: src,
    section: null,
    status: null,
    source: null,
    problems: [],
    b: [],
    q: [],
    passRule: "",
    fuDefs: [],
    fuRefs: [],
    refs: [],
  };
  src.forEach((text, i) => {
    const sec = text.match(RE_SECTION);
    if (sec) {
      out.section = sec[1];
      return;
    }
    if (!out.status && /^Status:/.test(text)) out.status = text;
    if (!out.source && /^Source:/.test(text)) out.source = text.slice(7).trim();
    if (out.section === "Behavioral Changes") {
      const b = text.match(RE_B);
      if (b) {
        out.b.push({
          line: i + 1,
          checked: b[1] !== " ",
          id: b[2],
          kind: b[3],
          label: b[4],
        });
        return;
      }
      if (RE_B_LOOSE.test(text)) {
        out.problems.push({
          line: i + 1,
          msg: `Behavioral Changes 行格式非法：${text.trim()}`,
          fix: "应为 `- [ ] B<n> [ADDED|MODIFIED|REMOVED]: <Requirement 名> / <Scenario 名>`",
        });
      }
    }
    if (out.section === "Quality") {
      const q = text.match(RE_Q);
      if (q) out.q.push({ line: i + 1, checked: q[1] !== " ", id: q[2], label: q[3] });
    }
    if (out.section === "Pass Rule") out.passRule += text + "\n";
    const fuDef = text.match(RE_FU_DEF);
    if (fuDef) out.fuDefs.push({ line: i + 1, id: fuDef[1] });
    for (const m of text.matchAll(RE_FU_REF)) out.fuRefs.push({ line: i + 1, id: m[1] });
    // 全文范围找 B/Q 引用，用来查悬空编号（含 Quality 行与 Pass Rule）。
    for (const m of text.matchAll(RE_B_REF)) out.refs.push({ line: i + 1, kind: "B", n: +m[1] });
  });
  return out;
}

const specFiles = existsSync(specsDir)
  ? readdirSync(specsDir, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => e.name)
      .sort()
  : [];

// 测试侧回链：按 capability 归属，因为 B 编号在每个 capability 内各自从 1 起。
const allTestFiles = walk(testsDir).filter((p) => p.endsWith(".go"));
const anchorsByCap = new Map();

for (const cap of specFiles) {
  const specPath = join(specsDir, cap, "spec.md");
  const contractPath = join(specsDir, cap, "contract.md");

  if (!existsSync(specPath)) {
    note(failures, rel(contractPath), 1, `缺少 spec.md`, `specs/${cap}/ 下应有 spec.md`);
    continue;
  }
  if (!existsSync(contractPath)) {
    note(failures, rel(specPath), 1, `缺少 contract.md`, `specs/${cap}/ 下应有 contract.md`);
    continue;
  }

  const spec = parseSpec(specPath);
  const contract = parseContract(contractPath);
  const specRel = rel(specPath);
  const capRel = rel(contractPath);

  // --- spec 结构 ---
  for (const sec of spec.sections) {
    if (!SPEC_SECTIONS.has(sec.name)) {
      note(
        failures,
        specRel,
        sec.line,
        `spec 出现 canon 未定义的章节 \`## ${sec.name}\``,
        "spec 只允许 `## Elaborates` / `## Requirements` / `## Coverage Gaps`",
      );
    }
  }

  const hasChangeMarker = /\b(ADDED|MODIFIED|REMOVED)\b/.test(spec.lines.join("\n"));
  if (hasChangeMarker) {
    const at = spec.lines.findIndex((l) => /\b(ADDED|MODIFIED|REMOVED)\b/.test(l)) + 1;
    note(
      failures,
      specRel,
      at,
      "live doc 里出现变更分类标记（ADDED/MODIFIED/REMOVED）",
      "变更分类只属于 contract.md；spec 描述目标状态，不留变更痕迹",
    );
  }

  for (const [name, req] of spec.requirements) {
    if (req.scenarios.length === 0) {
      note(
        failures,
        specRel,
        req.line,
        `Requirement「${name}」下没有任何 Scenario`,
        "每个 Requirement 至少一个 Scenario",
      );
    }
  }

  // --- contract 基本字段 ---
  if (!contract.status) {
    note(failures, capRel, 1, "缺少 `Status:` 行", "补 `Status: DRAFT` 或 `Status: APPROVED`");
  }
  if (!contract.source) {
    note(failures, capRel, 1, "缺少 `Source:` 行", "补 `Source: specs/<capability>/spec.md`");
  } else if (!existsSync(join(root, contract.source))) {
    note(failures, capRel, 1, `Source 指向的文件不存在：${contract.source}`, "改成实际 spec 路径");
  }
  for (const p of contract.problems) note(failures, capRel, p.line, p.msg, p.fix);

  // --- 编号唯一且连续 ---
  const checkSequence = (items, prefix, section) => {
    const seen = new Set();
    items.forEach((item, idx) => {
      const n = +item.id.slice(1);
      if (seen.has(n)) {
        note(failures, capRel, item.line, `${section} 的 ${item.id} 重复`, "编号在本份 contract 内必须唯一");
      }
      seen.add(n);
      if (n !== idx + 1) {
        note(
          failures,
          capRel,
          item.line,
          `${section} 的编号不连续：第 ${idx + 1} 条是 ${item.id}`,
          `${prefix} 编号从 1 起连续递增；重新发布时不复用旧号`,
        );
      }
    });
  };
  checkSequence(contract.b, "B", "Behavioral Changes");
  checkSequence(contract.q, "Quality", "Quality");

  if (contract.q.length === 0) {
    note(failures, capRel, 1, "Quality 区为空", "补 Q* 通用质量项");
  }
  // 防呆：解析不出 B 项时上面所有编号与标签检查都会空转透绿，必须显式报错。
  if (contract.b.length === 0) {
    note(
      failures,
      capRel,
      1,
      "Behavioral Changes 区没有解析出任何 B 项",
      "区块标题应为 `## Behavioral Changes`，条目应为 `- [ ] B1 [ADDED]: <Requirement 名> / <Scenario 名>`",
    );
  }

  // --- B 标签能定位到 spec 的 Requirement/Scenario ---
  const bIds = new Set(contract.b.map((b) => +b.id.slice(1)));
  for (const b of contract.b) {
    if (b.kind === "REMOVED") continue; // 行为已被删除，spec 里本就不该有
    const sep = b.label.indexOf(" / ");
    if (sep < 0) {
      note(
        failures,
        capRel,
        b.line,
        `${b.id} 的标签不是「Requirement 名 / Scenario 名」：${b.label}`,
        "标签写成 `<Requirement 名> / <Scenario 名>`，名字照抄 spec",
      );
      continue;
    }
    const reqName = b.label.slice(0, sep).trim();
    const scName = stripNote(b.label.slice(sep + 3));
    const req = spec.requirements.get(reqName);
    if (!req) {
      note(
        failures,
        capRel,
        b.line,
        `${b.id} 指向的 Requirement「${reqName}」在 ${specRel} 中不存在`,
        "核对拼写；spec 改名后 contract 标签要跟着改",
      );
      continue;
    }
    if (!req.scenarios.some((s) => s.name === scName)) {
      const near = req.scenarios.map((s) => s.name).join("、");
      note(
        failures,
        capRel,
        b.line,
        `${b.id} 指向的 Scenario「${scName}」在 Requirement「${reqName}」下不存在`,
        `该 Requirement 现有 Scenario：${near}`,
      );
    }
  }

  // --- 悬空编号引用 ---
  for (const ref of contract.refs) {
    if (ref.kind === "B" && !bIds.has(ref.n)) {
      note(
        failures,
        capRel,
        ref.line,
        `引用了不存在的 ${"B" + ref.n}`,
        "B 编号重排后，Quality / Pass Rule 里的引用要同步改",
      );
    }
  }
  const qIds = new Set(contract.q.map((q) => +q.id.slice(1)));
  for (const m of contract.passRule.matchAll(RE_Q_REF)) {
    if (!qIds.has(+m[1])) {
      note(failures, capRel, 1, `Pass Rule 引用了不存在的 Q${m[1]}`, "改引用或补该 Q 项");
    }
  }

  // --- FU：定义唯一，回指必须落在某个定义上 ---
  const fuIds = new Set();
  for (const fu of contract.fuDefs) {
    if (fuIds.has(fu.id)) {
      note(failures, capRel, fu.line, `FU-${fu.id} 重复定义`, "同一 FU 编号只定义一次，别处引用它");
    }
    fuIds.add(fu.id);
  }
  for (const ref of contract.fuRefs) {
    if (!fuIds.has(ref.id)) {
      note(failures, capRel, ref.line, `引用了未定义的 FU-${ref.id}`, "补定义或改编号");
    }
  }

  // --- 回链 ---
  const pattern = capPattern(cap);
  const owned = allTestFiles.filter((p) => pattern.test(rel(p)));
  const anchored = new Set();
  for (const p of owned) {
    lines(p).forEach((text, i) => {
      for (const m of text.matchAll(RE_ANCHOR)) {
        anchored.add(+m[1].slice(1));
        if (!bIds.has(+m[1].slice(1))) {
          note(
            failures,
            rel(p),
            i + 1,
            `回链 \`// contract: ${m[1]}\` 在 specs/${cap}/contract.md 里不存在`,
            "编号重排会让回链断掉；改回链或改 contract",
          );
        }
      }
    });
  }
  anchorsByCap.set(cap, anchored);

  for (const b of contract.b) {
    const n = +b.id.slice(1);
    if (b.checked && !anchored.has(n)) {
      note(
        failures,
        capRel,
        b.line,
        `${b.id} 已勾选，但没有回链用例认领它`,
        `在 ${cap}_spec_test.go 的用例上方补 \`// contract: ${b.id}\`，或改为未勾选`,
      );
    }
    if (!b.checked && anchored.has(n)) {
      note(
        warnings,
        capRel,
        b.line,
        `${b.id} 未勾选，但已有回链用例`,
        "用例已存在，复选框该勾上（devloop 把未勾选当待办）",
      );
    }
  }
}

// --- 反向：测试里的回链必须归属到某个 capability ---
const ownedByAnyCap = new Set();
for (const cap of specFiles) {
  for (const p of allTestFiles.filter((f) => capPattern(cap).test(rel(f)))) ownedByAnyCap.add(rel(p));
}
for (const p of allTestFiles) {
  lines(p).forEach((text, i) => {
    if (RE_ANCHOR.test(text) && !ownedByAnyCap.has(rel(p))) {
      note(
        failures,
        rel(p),
        i + 1,
        "该文件不在任何 capability 的命名约定下，回链无法判定归属",
        "spec 用例文件命名为 `<capability>_spec_test.go`，连字符与下划线互认",
      );
    }
    RE_ANCHOR.lastIndex = 0;
  });
}

const render = (list) =>
  list.length === 0
    ? []
    : list.flatMap((f) => [
        `  ${f.file}:${f.line}`,
        `    ${f.msg}`,
        ...(f.fix ? [`    ↳ ${f.fix}`] : []),
      ]);

if (warnings.length > 0) {
  console.log(`spec/contract 一致性：${warnings.length} 条提示\n`);
  console.log(render(warnings).join("\n"));
  console.log("");
}

if (failures.length > 0) {
  console.error(`spec/contract 一致性门禁未通过：${failures.length} 条\n`);
  console.error(render(failures).join("\n"));
  process.exit(1);
}

const totals = { b: 0, q: 0, anchored: 0 };
for (const cap of specFiles) {
  const contract = parseContract(join(specsDir, cap, "contract.md"));
  totals.b += contract.b.length;
  totals.q += contract.q.length;
  totals.anchored += anchorsByCap.get(cap)?.size ?? 0;
}

console.log(
  `spec/contract 一致性通过：${specFiles.length} 份 spec ↔ ${specFiles.length} 份 contract，` +
    `B ${totals.b} 条 / Q ${totals.q} 条 / 回链 ${totals.anchored} 条`,
);