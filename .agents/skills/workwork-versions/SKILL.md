---
name: workwork-versions
description: 跨仓基础镜像版本统一：改 versions.env、跑 scripts/sync-versions.sh check/sync、版本漂移排查。触发：修改基础镜像或语言版本（GO_VERSION、ALPINE_VERSION、BUN_VERSION、NGINX_VERSION、XRAY_VERSION）；CI 或 make check 因版本一致性校验失败；版本升级跨仓同步。
---

# workwork 跨仓基础镜像版本治理

本 skill 用于维护多站点跨仓基础镜像与运行环境的版本一致性。

## 架构事实

- **主本权威**：`workwork/versions.env` 是唯一主本。`media_center` 与 `music_player`（并列检出目录）的 `deploy/versions.env`、各 Dockerfile 的 `ARG` 默认值、CI workflow 中的 `go-version` / `bun-version`、compose 的 `:-` 兜底值均以主本为准。
- **管理点位**：
  - `workwork`：`versions.env`、`docker-compose.yml`、`services/*/Dockerfile`、`.github/workflows/*.yml`
  - `media_center`：`deploy/versions.env`、`media_server/Dockerfile`、`deploy/nginx/Dockerfile`、`.github/workflows/*.yml`
  - `music_player`：`deploy/versions.env`、`music_server/Dockerfile`、`netease_api/Dockerfile`、`deploy/nginx/Dockerfile`、`.github/workflows/*.yml`
- **站点专属键**：如 `PYTHON_VERSION` 只在 `media_center` 内部生效，不参与跨仓主本同步。

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
