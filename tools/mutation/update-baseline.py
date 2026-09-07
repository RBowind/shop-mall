#!/usr/bin/env python3
"""解析 gremlins 夜跑日志，维护 metrics 分支上的基线文档。

流程（CI 内）：
  1. 从 gremlins-report.log 提取汇总指标 + 存活变异体分布
  2. 重写 docs/metrics/mutation-baseline.md（趋势表追加一行，分布表刷新）
  3. 推送到孤儿分支 metrics（main 有分支保护，metrics 无）
  4. 判定回退（efficacy 较前夜下跌超阈值或跌破绝对下限）：
     回退则创建 GitHub Issue（带去重），并以退出码 1 让本次夜跑标红

本地验证：python3 tools/mutation/update-baseline.py <log> --dry-run
"""

import argparse
import os
import re
import subprocess
import sys
from collections import Counter, defaultdict
from datetime import datetime, timedelta, timezone

TZ8 = timezone(timedelta(hours=8))
BASELINE_PATH = "docs/metrics/mutation-baseline.md"
BRANCH = "metrics"
LABEL = "mutation-nightly"
# 回退口径：较前一晚 efficacy 下跌超过 1.0 个百分点，或绝对值跌破 70。
EFFICACY_DROP_MAX = 1.0
EFFICACY_FLOOR = 70.0

MUTANT_RE = re.compile(
    r"^\s*(KILLED|LIVED|NOT COVERED|TIMED OUT|NOT VIABLE|SKIPPED)\s+"
    r"([A-Z_]+) at (\S+?):\d+:\d+\s*$"
)
TREND_ROW_RE = re.compile(r"^\| (\d{4}-\d{2}-\d{2} \d{2}:\d{2}) \| ([\d.]+)%")


def run(cmd, check=True, capture=False):
    return subprocess.run(
        cmd, shell=isinstance(cmd, str), check=check,
        text=True, capture_output=capture,
    )


def parse_log(path):
    """返回 (metrics, lived_by_dir, lived_by_mutator)。"""
    metrics = {}
    by_dir = defaultdict(Counter)
    by_mutator = Counter()
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            m = MUTANT_RE.match(line)
            if m:
                status, mutator, file_ = m.groups()
                if status == "LIVED":
                    d = os.path.dirname(file_) or "(根目录)"
                    by_dir[d][mutator] += 1
                    by_mutator[mutator] += 1
                continue
            if line.startswith("Mutation testing completed in "):
                metrics["duration"] = line.removeprefix("Mutation testing completed in ").strip()
            elif (m := re.match(r"Killed: (\d+), Lived: (\d+), Not covered: (\d+)", line)):
                metrics["killed"], metrics["lived"], metrics["not_covered"] = map(int, m.groups())
            elif (m := re.match(r"Test efficacy: ([\d.]+)%", line)):
                metrics["efficacy"] = float(m.group(1))
            elif (m := re.match(r"Mutator coverage: ([\d.]+)%", line)):
                metrics["coverage"] = float(m.group(1))
    required = {"killed", "lived", "efficacy", "coverage"}
    if not required.issubset(metrics):
        sys.exit(f"日志缺少汇总指标 {required - set(metrics)}，疑似 gremlins 中途失败，跳过基线更新")
    return metrics, by_dir, by_mutator


def render(new_row, trend_rows, by_dir, by_mutator, first_run):
    lines = [
        "# 变异夜跑基线（自动维护，勿手改）",
        "",
        "> 由 `mutation-nightly.yml` 每天 02:30 更新（GMT+8）。",
        "> 口径：Test efficacy = 被杀死/超时变异体 ÷ 全部有效变异体；Mutator coverage = 被测试覆盖到的变异点比例。",
        "> 用法：趋势掉点或夜跑标红 → 看对应 run 的 artifact 原始日志，按下方分布表找漏网位置补测试。",
        "",
        "## 趋势",
        "",
        "| 日期 (GMT+8) | Efficacy | Coverage | Killed | Lived | Not covered | 用时 | vs 前夜 |",
        "|---|---|---|---|---|---|---|---|",
    ]
    for r in trend_rows + [new_row]:
        lines.append(
            f"| {r['date']} | {r['efficacy']:.2f}% | {r['coverage']:.2f}% "
            f"| {r['killed']} | {r['lived']} | {r['not_covered']} | {r['duration']} | {r['delta']} |"
        )
    lines += ["", "## 最新一晚：存活变异体在哪（按目录，前 15）", ""]
    top_dirs = sorted(by_dir.items(), key=lambda kv: -sum(kv[1].values()))[:15]
    lines += ["| 目录 | Lived | 主要漏网算子 |", "|---|---|---|"]
    for d, ctr in top_dirs:
        muts = ", ".join(f"{k} ×{v}" for k, v in ctr.most_common(3))
        lines.append(f"| {d} | {sum(ctr.values())} | {muts} |")
    lines += ["", "## 最新一晚：漏网算子分布", "", "| 算子 | Lived |", "|---|---|"]
    for k, v in by_mutator.most_common():
        lines.append(f"| {k} | {v} |")
    if first_run:
        lines += ["", "_首晚基线：明晚起自动对比回退。_"]
    return "\n".join(lines) + "\n"


def fetch_baseline():
    """从 metrics 分支取现有基线内容，返回 (内容, 分支是否存在)。"""
    r = run(["git", "fetch", "origin", BRANCH], check=False, capture=True)
    if r.returncode != 0:
        return "", False
    r = run(["git", "show", f"origin/{BRANCH}:{BASELINE_PATH}"], check=False, capture=True)
    return (r.stdout if r.returncode == 0 else ""), True


def extract_trend_rows(content):
    rows = []
    for line in content.splitlines():
        if m := TREND_ROW_RE.match(line):
            cells = [c.strip() for c in line.strip("|").split("|")]
            rows.append({"date": cells[0], "efficacy": float(cells[1].rstrip("%")),
                         "coverage": float(cells[2].rstrip("%")), "killed": cells[3],
                         "lived": cells[4], "not_covered": cells[5],
                         "duration": cells[6], "delta": cells[7]})
    return rows


def commit_and_push(md_content, branch_exists):
    if branch_exists:
        # 从已有 metrics 历史续接，push 保持快进。
        run(["git", "switch", "-c", f"{BRANCH}-tmp", f"origin/{BRANCH}"])
    else:
        run(["git", "switch", "--orphan", f"{BRANCH}-tmp"])
        run(["git", "rm", "-r", "--cached", "-q", "."], check=False)
    # 切分支后再落盘，避免未提交改动与 checkout 冲突。
    os.makedirs(os.path.dirname(BASELINE_PATH), exist_ok=True)
    with open(BASELINE_PATH, "w", encoding="utf-8") as f:
        f.write(md_content)
    run(["git", "add", BASELINE_PATH])
    run(["git", "-c", "user.name=github-actions[bot]",
         "-c", "user.email=41898282+github-actions[bot]@users.noreply.github.com",
         "commit", "-m", f"mutation nightly baseline {datetime.now(TZ8).date()}"])
    r = run(["git", "push", "origin", f"HEAD:{BRANCH}"], check=False, capture=True)
    if r.returncode != 0:
        sys.exit(f"推送 {BRANCH} 分支失败:\n{r.stderr}")


def open_regression_issue(metrics, prev, by_dir):
    drop = prev["efficacy"] - metrics["efficacy"]
    top = sorted(by_dir.items(), key=lambda kv: -sum(kv[1].values()))[:5]
    body = "\n".join([
        "昨晚变异夜跑发现测试网回退：",
        "",
        f"- Test efficacy：**{metrics['efficacy']:.2f}%**（前夜 {prev['efficacy']:.2f}%，跌 {drop:.2f} 个百分点）",
        f"- Lived {metrics['lived']} / Not covered {metrics['not_covered']} / Killed {metrics['killed']}",
        f"- 基线趋势与完整分布：[metrics 分支 {BASELINE_PATH}](https://github.com/{os.environ.get('GITHUB_REPOSITORY', 'RBowind/shop-mall')}/blob/{BRANCH}/{BASELINE_PATH})",
        "",
        "存活最多的目录：",
        "",
        "| 目录 | Lived |",
        "|---|---|",
        *[f"| {d} | {sum(c.values())} |" for d, c in top],
        "",
        "处理：下载本次 run 的 `gremlins-nightly-report` artifact，grep `LIVED` 定位漏网代码行，补断言。",
    ])
    title = f"变异夜跑回退：efficacy {prev['efficacy']:.2f}% → {metrics['efficacy']:.2f}%"
    r = run(f'gh issue list --state open --label {LABEL} --json title', capture=True)
    if title in r.stdout:
        return  # 同分数回退已有未关闭 Issue，避免刷屏
    run(["gh", "label", "create", LABEL, "--color", "D4C5F9",
         "--description", "变异夜跑判定测试网回退"], check=False)
    with open(".issue-body.md", "w", encoding="utf-8") as f:
        f.write(body)
    run(["gh", "issue", "create", "--title", title,
         "--label", LABEL, "--body-file", ".issue-body.md"])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("log")
    ap.add_argument("--dry-run", action="store_true", help="只打印基线文档，不提交不建 Issue")
    args = ap.parse_args()

    metrics, by_dir, by_mutator = parse_log(args.log)
    existing, branch_exists = fetch_baseline()
    trend_rows = extract_trend_rows(existing)

    today = datetime.now(TZ8).strftime("%Y-%m-%d")
    if trend_rows and trend_rows[-1]["date"].startswith(today):
        trend_rows.pop()  # 同日重跑：覆盖今天的行，对比对象仍是昨晚

    if trend_rows:
        prev = trend_rows[-1]
        drop = prev["efficacy"] - metrics["efficacy"]
        delta = f"{'+' if drop < 0 else '−'}{abs(drop):.2f}pt"
    else:
        prev, delta = None, "首晚基线"

    new_row = {"date": datetime.now(TZ8).strftime("%Y-%m-%d %H:%M"),
               "efficacy": metrics["efficacy"], "coverage": metrics["coverage"],
               "killed": metrics["killed"], "lived": metrics["lived"],
               "not_covered": metrics["not_covered"],
               "duration": metrics.get("duration", "?"), "delta": delta}
    md = render(new_row, trend_rows, by_dir, by_mutator, first_run=prev is None)

    regression = prev is not None and (
        metrics["efficacy"] < prev["efficacy"] - EFFICACY_DROP_MAX
        or metrics["efficacy"] < EFFICACY_FLOOR
    )

    if args.dry_run:
        print(md)
        print(f"[dry-run] 回退判定: {regression}", file=sys.stderr)
        return

    commit_and_push(md, branch_exists)

    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as f:
            f.write(f"## 变异夜跑\n\n- Efficacy **{metrics['efficacy']:.2f}%**（{delta}），"
                    f"Lived {metrics['lived']}，基线已更新到 `{BRANCH}` 分支\n")
            f.write("存活最多目录：" + ", ".join(
                f"{d}({sum(c.values())})" for d, c in sorted(by_dir.items(), key=lambda kv: -sum(kv[1].values()))[:5]) + "\n")

    if regression:
        open_regression_issue(metrics, prev, by_dir)
        sys.exit(f"回退：efficacy {prev['efficacy']:.2f}% → {metrics['efficacy']:.2f}%，已建 Issue")


if __name__ == "__main__":
    main()
