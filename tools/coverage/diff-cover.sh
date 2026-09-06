#!/usr/bin/env bash
# 第五道门：改动行覆盖率（diff coverage）。
# 口径只算本次改动行的覆盖率，不算全仓总量——防"凑数测试刷总量"。
# 管道：go test coverprofile -> gocover-cobertura(Cobertura XML, filename 对齐 git 路径)
#       -> diff-cover(与 base 分支 diff 比对，低于阈值退出码非 0)。
# 用法: diff-cover.sh [compare-branch] [fail-under]   # 默认 origin/main 80
# CI 对应 .github/workflows/pull-request-checks.yml 的 coverage job；本地跑法相同：
#   make diff-coverage
set -euo pipefail

COMPARE="${1:-origin/main}"
FAIL_UNDER="${2:-80}"

command -v diff-cover >/dev/null 2>&1 || {
  echo "diff-cover not found: pip install diff-cover" >&2
  exit 2
}

ROOT="$(git rev-parse --show-toplevel)"
GOBIN_DIR="$(go env GOPATH)/bin"
go install github.com/boumenot/gocover-cobertura@v1.5.0

# 转换器必须在 backend/ 内运行：它靠模块根目录解析包信息，
# 在仓库根跑会静默产出空 XML，让门变成永远绿的假象。
(
  cd "$ROOT/backend"
  go test ./... -count=1 -covermode=atomic -coverprofile=cover.out
  "$GOBIN_DIR/gocover-cobertura" < cover.out \
    | sed 's|filename="|filename="backend/|' > "$ROOT/coverage.xml"
)

# 空 XML 判红，堵住"没有可比对内容"的 vacuous pass。
grep -q '<class ' "$ROOT/coverage.xml" || {
  echo "coverage.xml has no class entries; refusing to pass vacuously" >&2
  exit 3
}

diff-cover "$ROOT/coverage.xml" --compare-branch="$COMPARE" --fail-under="$FAIL_UNDER"
