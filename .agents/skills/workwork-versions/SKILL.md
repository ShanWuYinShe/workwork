---
name: workwork-versions
description: 跨仓基础镜像版本统一：改 versions.env、跑 scripts/sync-versions.sh check/sync、版本漂移排查。触发：修改基础镜像或语言版本（GO_VERSION、ALPINE_VERSION、BUN_VERSION、NGINX_VERSION、XRAY_VERSION）；CI 或 make check 因版本一致性校验失败；版本升级跨仓同步。
---

# workwork 跨仓基础镜像版本治理

本 skill 用于维护多站点跨仓基础镜像与运行环境的版本一致性。

## 架构事实

- **主本权威**：`workwork/versions.env` 是唯一主本。并列检出目录（`media_center` / `music_player` / `ops-portal` / `hotspot`）的 `versions.env`、各 Dockerfile 的 `ARG` 默认值、CI workflow 中的 `go-version` / `bun-version`、compose 的 `:-` 兜底值、`.env.example` / `.env.prod.example` 的版本对照值（活动行与 `# KEY=值` 注释行）均以主本为准。
- **管理点位**：
  - `workwork`：`versions.env`、`docker-compose.yml`、`services/*/Dockerfile`、`.github/workflows/*.yml`、`.env.example`
  - `media_center`：`deploy/versions.env`、`media_server/Dockerfile`、`deploy/nginx/Dockerfile`、`.github/workflows/*.yml`、`deploy/.env.example`
  - `music_player`：`deploy/versions.env`、`music_server/Dockerfile`、`netease_api/Dockerfile`、`deploy/nginx/Dockerfile`、`.github/workflows/*.yml`、`deploy/.env.example`
  - `ops-portal`：`deploy/versions.env`、`compose` 两份、`services/*/Dockerfile`、`.github/workflows/*.yml`、`.env.example` / `.env.prod.example`
  - `hotspot`：`deploy/versions.env`、`backend-go/Dockerfile*`、`frontend/Dockerfile`、两份 compose、`.github/workflows/*.yml`、`.env.example`
- **`.env.example` 纪律**：版本对照值保持注释态（`# GO_VERSION=值`）——`cp .env.example .env` 后若为活动行，compose 读 `.env` 的优先级高于 `${VAR:-默认值}`，会让本地构建偏离统一默认值（历史上出现过 GO_VERSION 漂移）。脚本会按注释态原位改写，不会把它取消注释。
- **站点专属键**：如 `PYTHON_VERSION` 只在 `media_center` 内部生效，不参与跨仓主本同步。
- **运行前提**：站点仓须与本仓并列检出（本地 `~/work` 与服务器 `~` 布局一致）；缺失的站点目录自动跳过并告警，不判失败；可用 `SITES` 环境变量覆盖定位（见脚本头部注释）。在 worktree 内跑同步时，需用 `SITES` 显式指向各站点仓的 worktree 路径（默认的并列目录探测会落在 `.worktrees/` 下而不是站点仓根）。

## 常用命令

```bash
# 全矩阵一致性校验（漂移即退出 1，已挂入 make check）
./scripts/sync-versions.sh check

# 预览将要刷写的点位（只读，不落盘）
./scripts/sync-versions.sh sync --dry

# 将主本版本号刷写到所有管理点位
./scripts/sync-versions.sh sync
```

## 版本升级标准流程

1. 在 `workwork` 工作树中修改 `versions.env` 目标版本号。
2. 运行 `./scripts/sync-versions.sh sync` 将新版本号全量刷写到各点位。
3. 运行 `./scripts/sync-versions.sh check` 验证全矩阵一致性。
4. 运行 `make check` 验证当前仓库构建与测试。
5. 脚本仅改动文件内容、不操作 Git；根据改动范围，在 `workwork` 及受影响的站点仓库分别走各自的 Worktree 提交流程。
