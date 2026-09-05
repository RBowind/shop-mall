# 全局主键切换 UUID Follow-ups

- FU-8c3e5d21：存量数据迁移执行方式（就地映射表换算 vs 重建库重放迁移与种子）与回填窗口。
- FU-2d7b49f0：服务层 UUIDv7 生成的库选型与封装位置（Go 标准库不含 v7 生成）。
- FU-5e91a6b8：UUID 文本在 `request_hash` 规范化中的编码口径（大小写、连字符形态、空值段表示）发布前冻结；`event_key` 拼接口径已在规格中冻结。
- FU-1c7a9e43：`request_hash` 输入改取 UUID 文本后，存量订单的重放判定兼容策略。旧存量行的 hash 按旧编码计算，新代码算出的 hash 恒不匹配；`docs/tech-specs/data-model.md` 的约束是变更必须兼容存量订单的重放判定，否则旧 `client_token` 重放全部误判 409。
