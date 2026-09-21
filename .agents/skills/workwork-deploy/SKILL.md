---
name: workwork-deploy
description: workwork 构建与部署细节：Makefile 入口与路径映射、镜像版本烙印与健康端点、子模块指针纪律、容器权限归属（uid 1001 / nginx root）。触发：跑 make build/up/restart/logs/deploy-changed；改版本烙印或健康端点；子模块指针漂移；容器权限异常排查。
---

# workwork 构建与部署

## 构建 & 部署

### Makefile（部署/验证的可执行入口）

根目录 `Makefile` 把本节命令与部署映射封装为规则，AI 与人工优先用 make，避免每次凭记忆拼写：

- `make check` — 本地门禁：gofmt / go vet / go test / 前端构建（admin 改动的最低验证项）
- `make build <svc...>` / `make up <svc...>` / `make restart <svc...>` / `make logs <svc...>` — compose 薄封装
- `make submodule` — 子模块同步到主仓库记录的指针
- `make deploy-changed` — 只在服务器执行的一键部署（逻辑在 `scripts/deploy-changed.sh`）：工作区校验 → `git pull --ff-only` → **子模块强制同步**（缺失子模块会被 Docker 把挂载源建成目录，必须先于任何 up）→ 按变更推导受影响服务 → 最小 build/up；支持 `scripts/deploy-changed.sh <old-commit>` 复跑模式

路径 → 服务映射的唯一来源是 `scripts/deploy-changed.sh`，改动映射时只改脚本（本文件与 README 只描述行为，不复述映射表）。

### 版本与构建约定

- 版本号烙进镜像：Makefile 与 `deploy-changed.sh` 自动导出各仓库 `git describe`，compose 经 build args 传入（直接 `docker compose build` 不经 make/脚本时版本为 "dev"，发布构建必须经 make）。健康端点均携带版本：`/api/admin/health`、`/api/media/health`、`/api/music/health`、vod_extractor `/health`（含 yt-dlp 版本，YouTube 排障关键字段）。OCI 标签并非所有镜像都有：admin/nginx/xray/media_server/music_server 有 `org.opencontainers.image.revision` 标签，docker_proxy 仅 `image.version`，netease_api 无——查版本优先用健康端点。
- 版本号（media_app）：由子模块 services/media_center 的 git tag 驱动，tag 打在子模块仓库（非外层 workwork）；升版规则、发版流程与 APK 部署以该子模块 `media_app/AGENTS.md` 为唯一维护处，此处不复述。tag 位置口径：commit 在对应 Worktree 内完成，合并进主分支后 tag 与 push（含 tag）一律在主工作树执行（与全局 Git 准则一致）。
- JDK（media_app 构建用）：以全局「本机环境」Android Studio JBR 条目为准（使用前以 ls 验证，变动时用 mdfind 定位）；Gradle 命令一律加 `--no-daemon`（Daemon 构建后 idle 常驻，多次构建会撑爆内存）。APK 有 normal + legacy（Android 4.4.2 / API 19）两变体，构建与上传步骤见 README「部署流程」media_app 条。
- 子模块注意：`git pull` 不会更新子模块指针，手动部署时必须补 `git submodule update --init --recursive`，并用 `git submodule status` 检查漂移（行首 `+` 号 = 指针对应源码有更新），否则 build 出的仍是旧子模块源码（`make deploy-changed` 已自动处理）。子仓库的**纯文档 commit** 同样要推进指针：子仓库 AGENTS.md 修订合并推送后，回本仓库 `git submodule update --remote <path>` 推进指针并提交——否则在本仓库工作的 AI 读到的是子模块旧文档。

### 权限注意（首次部署或权限异常时）

- `.wework/` 和 `logs/` 必须归 uid=1001 (workuser) 所有（admin 以 user:1001:1001 运行）
- 但 `logs/nginx/` 必须归 root（nginx master 以 root 写日志，cap_drop:ALL 无 DAC_OVERRIDE）
- 修复：`sudo chown -R 1001:1001 .wework/ logs/ && sudo chown -R root:root logs/nginx/`
