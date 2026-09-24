---
name: workwork-deploy
description: workwork 构建与部署细节：Makefile 入口与路径映射、镜像版本烙印与健康端点、容器权限归属（uid 1001 / nginx root）。触发：跑 make build/up/restart/logs/check/deploy-changed；改版本烙印或健康端点；容器权限异常排查；部署后版本指纹核对失败。
---

# workwork 构建与部署

> **历史提醒**：media/music 已于拆分时移出本仓库（现为独立站仓库 `media_center` / `music_player`，与本项目零互通）。本文档曾描述「子模块（submodule）指针纪律」，但本仓库**已无任何子模块**（`.gitmodules` 不存在、`git submodule status` 为空、`scripts/` 与 `Makefile` 中无 submodule 逻辑、`services/` 下只有 admin/docker_proxy/nginx/xray）。子模块相关内容已删，不要再按子模块理解本仓库结构。

## 构建 & 部署

### Makefile（部署/验证的可执行入口）

根目录 `Makefile` 把本节命令与部署映射封装为规则，AI 与人工优先用 make，避免每次凭记忆拼写：

- `make check` — 本地门禁：gofmt / go vet / go test / 前端格式与构建（admin 改动的最低验证项）
- `make build <svc...>` / `make up <svc...>` / `make restart <svc...>` / `make logs <svc...>` — compose 薄封装
- `make ps` — 查看服务状态
- `make deploy-changed` — 只在服务器执行的一键部署（逻辑在 `scripts/deploy-changed.sh`）：工作区校验 → `git pull --ff-only` → 按变更推导受影响服务 → 最小 build/up → 部署后自校验（等 healthy + 核对版本指纹，**不通过即自动回滚并外发通知**）；支持 `scripts/deploy-changed.sh <old-commit>` 复跑模式

当前 compose 服务：`nginx`、`admin`、`xray`、`docker-proxy`。

路径 → 服务映射的唯一来源是 `scripts/deploy-changed.sh`，改动映射时只改脚本（本文件与 README 只描述行为，不复述映射表）。

### 版本与构建约定

- 版本号烙进镜像：Makefile 与 `deploy-changed.sh` 自动导出各服务的 `git describe`，compose 经 build args 传入。**直接 `docker compose build` 不经 make/脚本时版本为 "dev"，发布构建必须经 make。**
- **健康端点（以脚本为准，不要凭记忆假设）**：目前只有 `admin` 暴露带版本的健康端点 `/api/admin/health`（`services/admin/main.go:121`，返回 JSON 含 `version`）；`deploy-changed.sh` 的 `health_version()` **也只对 admin 做版本指纹核对**，其余服务直接 `return 0`。nginx 用 `/nginx-health`（返回 204，仅存活探测，不含版本）。查版本优先用健康端点，不要依赖镜像 OCI 标签（并非所有镜像都有）。
- 版本指纹核对期望值的来源同样在 `scripts/deploy-changed.sh`，改版本烙印逻辑时两边要一起看。

### 权限注意（首次部署或权限异常时）

- `.wework/` 和 `logs/` 必须归 uid=1001 (workuser) 所有（admin 以 user:1001:1001 运行）
- 但 `logs/nginx/` 必须归 root（nginx master 以 root 写日志，cap_drop:ALL 无 DAC_OVERRIDE）
- 修复：`sudo chown -R 1001:1001 .wework/ logs/ && sudo chown -R root:root logs/nginx/`

## 相关约定（不在此复述）

- 跨仓基础镜像版本统一（`versions.env` 主本、`scripts/sync-versions.sh` check/sync）：见 skill `workwork-versions`
- Docker 网络拓扑与安全（ipvlan L3 / docker-proxy 白名单 / RBAC）：见 skill `workwork-security`
- JDK / Gradle 等工具链约束：见 skill `toolchain-setup` 与 `local-environment`
