# 发布与恢复

本文件只回答两个问题：怎么发上去，出事怎么退回来。脚本都在 `deploy/release/`，本文不复述参数细节，以脚本头部的注释为准。

| 字段 | 值 |
|---|---|
| 负责服务 | backend（单体） |
| 更新日期 | 2026-09-16 |

## 发布路径

本地验证 → PR 五道门（`.github/workflows/pull-request-checks.yml`）→ 合入 main → `release-artifacts.yml` 构建后端镜像（不可变 tag）→ 手动执行发布脚本。

当前没有一键发布：发布与回滚是手动跑脚本，CI 只负责把门和产镜像。一键化是有意推迟的，见 decision-log「CI 门禁」条的边界。

## 发布前：preflight.sh

`deploy/release/preflight.sh`，五道检查，任一不过就停：

1. 镜像 tag 不可变（拒绝 latest/dev/test/staging）
2. compose 配置有效
3. 迁移版本与期望的 forward 版本一致
4. 备份新鲜度：全量备份 ≤ 24h，WAL 归档 ≤ 1h
5. 磁盘够放一次备份加一个新镜像

需要连活库的检查，在 postgres 没起时降级为 warning，不挡路。

## 发布后：smoke.sh

`deploy/release/smoke.sh`，按序探：

- `/health/live`、`/health/ready`
- 买家微信登录（没配真实 AppID 时用 fake client）
- 公开商品浏览
- 登录买家查积分（`/me`）

## 回滚：rollback.sh

`deploy/release/rollback.sh <previous-image-tag>`：切回上一个不可变镜像 tag，重启，重跑健康检查和 smoke。

**永不 `goose down`**。迁移只朝前（见 decision-log「数据库」条）：库结构出问题，写一条新的 forward migration 修，不回退。代码级回滚只换镜像 tag，一分钟的事。

## 备份与恢复演练

`deploy/backup/`：`backup-db.sh` 全量 + WAL 归档，`restore.sh` 恢复，`drill.sh` 配 `docker-compose.drill.yml` 做恢复演练。恢复能力以演练为准，不以脚本存在为准。

## 监控与告警

`deploy/prometheus/`、`deploy/alertmanager/`。告警规则的细节不在本文展开，以那两个目录为准——同一件事只放一个地方。

## 边界

- 单台 2C4G 单点，健康检查、备份、降级预案是必配不是选配（decision-log「部署」条）。
- 小程序端的发布（企业主体、类目资质、平台审核）不在这条链路里，归 `docs/architecture/01-miniapp.md`。
