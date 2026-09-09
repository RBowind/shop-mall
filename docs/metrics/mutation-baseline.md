# 变异夜跑基线（自动维护，勿手改）

> 由 `mutation-nightly.yml` 每天 02:30 更新（GMT+8）。
> 口径：Test efficacy = 被杀死/超时变异体 ÷ 全部有效变异体；Mutator coverage = 被测试覆盖到的变异点比例。
> 用法：趋势掉点或夜跑标红 → 看对应 run 的 artifact 原始日志，按下方分布表找漏网位置补测试。

## 趋势

| 日期 (GMT+8) | Efficacy | Coverage | Killed | Lived | Not covered | 用时 | vs 前夜 |
|---|---|---|---|---|---|---|---|
| 2026-09-08 08:32 | 77.60% | 84.13% | 1164 | 336 | 283 | 2 hours 47 minutes | 首晚基线 |
| 2026-09-09 07:47 | 77.60% | 84.13% | 1164 | 336 | 283 | 2 hours 25 minutes | −0.00pt |
| 2026-09-10 07:35 | 77.60% | 84.13% | 1164 | 336 | 283 | 2 hours 28 minutes | −0.00pt |

## 最新一晚：存活变异体在哪（按目录，前 15）

| 目录 | Lived | 主要漏网算子 |
|---|---|---|
| internal/admin | 91 | CONDITIONALS_NEGATION ×55, CONDITIONALS_BOUNDARY ×28, ARITHMETIC_BASE ×7 |
| internal/config | 35 | CONDITIONALS_BOUNDARY ×15, CONDITIONALS_NEGATION ×13, ARITHMETIC_BASE ×7 |
| cmd/server | 31 | CONDITIONALS_NEGATION ×30, CONDITIONALS_BOUNDARY ×1 |
| internal/platform/logging | 30 | CONDITIONALS_NEGATION ×14, CONDITIONALS_BOUNDARY ×8, ARITHMETIC_BASE ×7 |
| tests/e2e | 25 | CONDITIONALS_NEGATION ×19, ARITHMETIC_BASE ×4, CONDITIONALS_BOUNDARY ×1 |
| internal/product | 23 | CONDITIONALS_NEGATION ×14, CONDITIONALS_BOUNDARY ×8, ARITHMETIC_BASE ×1 |
| internal/user | 19 | CONDITIONALS_BOUNDARY ×13, CONDITIONALS_NEGATION ×4, ARITHMETIC_BASE ×2 |
| internal/storage | 14 | CONDITIONALS_BOUNDARY ×11, CONDITIONALS_NEGATION ×3 |
| internal/application/order | 13 | CONDITIONALS_NEGATION ×8, CONDITIONALS_BOUNDARY ×5 |
| internal/middleware | 9 | CONDITIONALS_BOUNDARY ×5, CONDITIONALS_NEGATION ×4 |
| internal/application/points | 7 | CONDITIONALS_NEGATION ×4, CONDITIONALS_BOUNDARY ×3 |
| internal/application/auth | 6 | CONDITIONALS_NEGATION ×3, CONDITIONALS_BOUNDARY ×3 |
| internal/application/refund | 6 | CONDITIONALS_NEGATION ×4, CONDITIONALS_BOUNDARY ×2 |
| internal/cart | 5 | CONDITIONALS_NEGATION ×2, CONDITIONALS_BOUNDARY ×2, ARITHMETIC_BASE ×1 |
| internal/order | 5 | CONDITIONALS_NEGATION ×4, ARITHMETIC_BASE ×1 |

## 最新一晚：漏网算子分布

| 算子 | Lived |
|---|---|
| CONDITIONALS_NEGATION | 188 |
| CONDITIONALS_BOUNDARY | 111 |
| ARITHMETIC_BASE | 33 |
| INVERT_NEGATIVES | 3 |
| INCREMENT_DECREMENT | 1 |
