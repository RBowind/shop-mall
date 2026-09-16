# 变更提案

功能、修 bug 或调整业务口径前，先在对应模块目录记录提案，再由人拍板后实现。

## 目录约定

```text
docs/proposals/<module>/<short-name>.md
```

一个提案只解决一件事。提案至少写清：

- 动机与范围
- 现状取证
- 目标行为与验收方式
- 不做什么
- 仍需拍板的 Follow-ups

拍板后的长期决策进入 `docs/decisions/`，当前行为进入 `specs/<capability>/spec.md`，实现细节进入对应技术规格或设计文档。不要把提案当作当前行为契约。
