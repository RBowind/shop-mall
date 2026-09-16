# 行为验证缺口

这些行为在两处都说得清清楚楚——spec 里有 Scenario，代码里有实现——但**没有认领它们的测试用例**。

它不是「未定行为」：那是各 spec 的 `## Coverage Gaps` 管的事，管的是口径还没定下来的行为。这里的行为口径是定的，缺的是证据。

## 这条账是怎么来的

`specs/<capability>/contract.md` 的 Behavioral Changes 里，**未勾选、且没有 `// contract: B<n>` 回链**的条目。

contract 复选框是交付进度的唯一记录（`devloop.md:38` 原话：「contract 复选框是唯一进度记录，不另建状态文件」）。没勾选就是没交付；一条 B 没有回链用例认领，就说明没有任何测试在验证它。

shop-mall 是先有代码、后有 spec 的存量仓库，而 sddspec 的 contract 假定的是先有 spec、后有代码。所以这些条目的行为早就跑在线上，只是从没被契约锚定过。

## 为什么 devloop 接不了这笔账

devloop 的循环是「写失败测试 → 独立评审 → 实现转绿 → 勾选」。这些行为已经实现，新写的测试会立刻通过，拿不到有效红灯。遇到这种情况：

- `devloop.md:86` —— `ALREADY_IMPLEMENTED：你亲自重跑后停止，请用户核对 contract`
- `devloop.md:113` —— 只有红绿证据齐全才能勾选

于是它自己走不出去。存量回填天然不进这条循环，得单独排。

## 当前规模

截至 2026-09-17：

| capability | B 总数 | 已锚定 | 缺口 |
|---|---|---|---|
| admin-authz | 33 | 0 | 33 |
| buyer-auth | 13 | 6 | 7 |
| cart | 9 | 8 | 1 |
| catalog | 20 | 0 | 20 |
| checkout | 23 | 0 | 23 |
| coupon | 23 | 0 | 23 |
| fulfillment | 12 | 0 | 12 |
| order-payment | 13 | 0 | 13 |
| points | 13 | 0 | 13 |
| refund | 14 | 0 | 14 |
| **合计** | **173** | **14** | **159** |

这张表的数字会随进展变化，别拿它当准数——下面那条命令才是。

## 查当前清单

```bash
node tools/spec/check-spec-contract.mjs --report
```

它列出每个 capability 未勾选且无回链的 B 编号。这门禁已经在 CI 的 contract job 里跑（`make spec-check`），所以清单随时可复现。

## 怎么关掉一条

1. 写一个用例，断言该 B 对应的 spec Scenario 的可观测结果。
2. 用例上方写回链注释 `// contract: B<n>`。写法见 `team-test-standards-backend`。
3. 跑通后，把 `contract.md` 里那条 B 的复选框勾上。

一条 B 只由一个用例认领（`contract.md` 里 B 标签的 Scenario 名是唯一锚点，用例与它一一对应）。

## 排期口径

优先级按业务风险定，不按 capability 顺序。缺口的分布不均匀——`cart` 只剩 1 条，`admin-authz` 有 33 条——但这不是排序依据；金额、权限、并发这几类行为即使只有一条没测试，风险也高于其余全部。