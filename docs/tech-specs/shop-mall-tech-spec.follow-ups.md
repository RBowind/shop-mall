# shop-mall 系统 Tech Spec Follow-ups

- FU-3d7b8c1a：将 `GET /api/admin/v1/users` 与 `GET /api/admin/v1/audit-logs` 补入 `docs/api/openapi.yaml`（两端点已在后端路由注册但契约源缺失）。
- FU-5b9c4e27：`docs/api/openapi.yaml` 的 `adminUpdateProduct` 响应缺 422 声明（其请求体 `ProductWriteRequest.category` 描述与后端实现均按未知目录键 422 处理），补入以与 `adminCreateProduct` 对齐。
- FU-8e2a5d7c：实现积分对账任务前需定：触发周期与配置项、告警输出形态、冻结状态的存储落点、解冻入口，以及冻结范围（`02-backend.md` 写"相关运营操作"、`05-database.md` 写"相关积分调整"，两处不一致需裁决）。
- FU-1c6f9b3e：定义展示与日志脱敏的具体格式（如手机号保留位），当前各文档只有脱敏字段清单，管理端订单列表"默认脱敏展示"无落地规则。
- FU-7a4d2e8b：明确管理员登录失败（401/429）归属的业务错误码段（2xxx 段现定义为微信认证与登录态过期）。
