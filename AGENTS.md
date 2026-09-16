# workwork

Docker 代理基础设施：Xray VLESS Reality + XHTTP 代理 / Nginx 反代 / Go 管理面板。

通用约束（Git 规范、Commit 格式、验证流程、工具链偏好、Docker 等）以全局 `~/AGENTS.md` 为准（各 AI 工具的全局指令文件均链接到该文件），本文件只做项目特有补充，不得放宽或违背全局约束。

AI Agent 工作目录：所有 AI agent 工作期间产生的中间文件写入项目根 `.workwork/`（已 `.gitignore`）。

## 硬约束

所有服务功能必须隔离在 Docker 容器内。 禁止修改宿主机环境设置（iptables、sysctl、网络配置），创建 Docker 网络除外。IPv4 路由走 Docker bridge NAT；IPv6 路由走 ipvlan L3。admin 服务通过 `docker.sock` 和 `containerExec()` 操作其他容器。

## 构建 & 部署

### Makefile（部署/验证的可执行入口）

根目录 `Makefile` 把本节命令与部署映射封装为规则，AI 与人工优先用 make，避免每次凭记忆拼写：

- `make check` — 本地门禁：gofmt / go vet / go test / 前端构建（admin 改动的最低验证项）
- `make build <svc...>` / `make up <svc...>` / `make restart <svc...>` / `make logs <svc...>` — compose 薄封装
- `make submodule` — 子模块同步到主仓库记录的指针
- `make deploy-changed` — 服务器一键部署（逻辑在 `scripts/deploy-changed.sh`）：工作区校验 → `git pull --ff-only` → **子模块强制同步**（缺失子模块会被 Docker 把挂载源建成目录，必须先于任何 up）→ 按变更推导受影响服务 → 最小 build/up；支持 `scripts/deploy-changed.sh <old-commit>` 复跑模式

路径 → 服务映射在 `scripts/deploy-changed.sh` 与本节注释两处维护，改动映射时必须同步。

### JDK 约定（media_app 涉及）

JDK 说明见全局。构建命令：

```bash
# JAVA_HOME 见全局「本机环境」Android Studio JBR 条目（以 ls 验证，变动时用 mdfind 定位）
export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
services/media_center/media_app/gradlew -p services/media_center/media_app :app:compileDebugKotlin   # 编译检查（最快）
services/media_center/media_app/gradlew -p services/media_center/media_app :app:assembleDebug        # 出 APK
```

```bash
# Go 编译检查
go build -C services/admin -o /dev/null .

# Go 验证
go vet -C services/admin ./...         # 静态检查
gofmt -l services/admin/               # 格式化检查（列出不符合的文件）
go test -C services/admin ./...        # 单元测试

# Svelte 前端构建（使用 Bun；前端源码在 services/admin/ui/，构建产物打进 admin 镜像）
bun install --cwd services/admin/ui                   # 安装依赖（生成/校验 bun.lock）
bun run --cwd services/admin/ui build                 # 构建

# Media Server 验证（源码在 services/media_center 子模块；首次需 git submodule update --init --recursive）
go build -C services/media_center/media_server -o /dev/null .
go vet -C services/media_center/media_server ./...
go test -C services/media_center/media_server ./...

# Music Server 验证（源码在 services/music_player 子模块的 music_server/）
go build -C services/music_player/music_server -o /dev/null .
go vet -C services/music_player/music_server ./...
go test -C services/music_player/music_server ./...

# netease_api(网易云上游):纯 Dockerfile 打包 npm 包,无本地源码,验证即 compose build

# 部署（git push 后在服务器执行；用户明确要求时，agent 可执行 SSH 部署）
# 首选：ssh workwork-server "cd workwork && make deploy-changed"
#   （等价于 scripts/deploy-changed.sh：pull + 子模块同步 + 按变更最小 build/up）
# 原则：先判断 git pull 带来的文件变更，只 build / recreate 受影响服务；
# 禁止无脑 --no-cache 全量 build 或 docker compose up -d 全量重建。
# 注意：git pull 不会更新子模块指针，手动部署时必须补
#   git submodule update --init --recursive
# 并用 git submodule status 检查漂移（行首 + 号 = 指针对应源码有更新），
# 否则 build 出的仍是旧子模块源码。
#
# 权限注意：
#   .wework/ 和 logs/ 必须归 uid=1001 (workuser) 所有（admin 以 user:1001:1001 运行）
#   但 logs/nginx/ 必须归 root（nginx master 以 root 写日志，cap_drop:ALL 无 DAC_OVERRIDE）
#   首次部署或权限异常时：
#     sudo chown -R 1001:1001 .wework/ logs/ && sudo chown -R root:root logs/nginx/
ssh workwork-server "cd workwork && make deploy-changed"

# 根据变更选择最小部署命令（通常不加 --no-cache；除非依赖缓存明确异常）：
# - 版本号（media_app）：由子模块 services/media_center 的 git tag 驱动（见该仓库 AGENTS.md
#   “版本号规范”）。构建期自动派生 versionName/versionCode；AI 改完代码、发版前在子模块打
#   tag 即“自动更新版本”：git tag vX.Y.Z（tag 打在子模块仓库，非外层 workwork）。
#   发版流程：commit / tag 在对应 Worktree 内完成；合并进主分支后，push（含 tag）一律在主工作树执行，
#   见全局 Git 准则。无需手改 build.gradle。升版规则：major=破坏性 / minor=新功能 / patch=修复。
# - services/media_center/media_app/**（Android APK，normal + legacy 两变体）:
#     # --no-daemon 必加：Gradle/Kotlin Daemon 构建后 idle 保留数小时不退出（各 -Xmx2g），
#     # AI agent 多次构建会累积多个 java 进程撑爆内存。--no-daemon 让构建完 java 进程立即退出。
#     # release（非 debug）：已配 debug 签名 + R8 minify，体积减半且可直接安装。
#     # JAVA_HOME 见全局「本机环境」（以 ls 验证，变动时用 mdfind 定位）
#     # normal 变体:
#     JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home" \
#       services/media_center/media_app/gradlew -p services/media_center/media_app :app:assembleRelease --no-daemon && \
#     scp services/media_center/media_app/app/build/outputs/apk/release/app-release.apk \
#       workwork-server:workwork/.wework/generated/media_center/media-center-app-v<vc>-<sha12>.apk
#     # legacy 变体（Android 4.4.2 / API 19）:
#     JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home" \
#       services/media_center/media_app_legacy/gradlew -p services/media_center/media_app_legacy :app:assembleRelease --no-daemon && \
#     scp services/media_center/media_app_legacy/app/build/outputs/apk/release/app-release.apk \
#       workwork-server:workwork/.wework/generated/media_center/media-center-app-legacy-v<vc>-<sha12>.apk
#     # 上传后手写 app-update.json / app-update-legacy.json（含 versionCode/versionName/
#     # apkFilename/sizeBytes/sha256/releaseNotes），或在管理面板“Android TV App 更新发布”
#     # 页面选变体后上传（admin 自动写 manifest + APK）。
# - services/media_center/media_server/**（git submodule）:
#     docker compose build media_server && docker compose up -d --no-deps media_server
# - services/media_center/vod_extractor/**（git submodule；YouTube yt-dlp 解析。
#     播放硬前提：宿主机 .wework/vod_extractor/youtube-cookies.txt 必须存在
#     （Netscape 格式 cookies，过期需重新导出），否则解析全被 bot 检测拦截）:
#     docker compose build vod_extractor && docker compose up -d --no-deps vod_extractor
#     （搜索与解析都走它，无 Data API 配额；解析服务自身一般无需改动）
# - services/music_player/music_server/**（git submodule；曲库在宿主机
#     .wework/music-library/，管理面板「音乐曲库」页上传或手动投放，扫描
#     TTL 内生效；网络源上游失败自动降级仅本地）:
#     docker compose build music_server && docker compose up -d --no-deps music_server
# - services/netease_api/** 或 music_server 需要网络源时:
#     docker compose build netease_api music_server && \
#     docker compose up -d --no-deps netease_api music_server
#     （可选 .env 配 NETEASE_MUSIC_COOKIE 解锁登录态曲目；匿名仅免费曲）
# - services/admin/**（含 ui/ 与 configs/ 模板，均随 admin 镜像分发）:
#     docker compose build admin && docker compose up -d --no-deps admin
# - services/nginx/**:
#     docker compose build nginx && docker compose up -d --no-deps nginx
# - services/xray/**:
#     docker compose build xray xray-forward && docker compose up -d --no-deps xray xray-forward
# - docker-compose.yml / 共享网络 / 共享 env 变更:
#     docker compose up -d <受影响服务...>
# admin 的 deploy.go / configs 模板改动可能影响运行时生成的 nginx/xray/media 配置；
# admin 重启后只重启实际配置变化的下游服务。
```

## 架构速览

完整设计叙事（服务分工、网络拓扑、IPv6 寻址、RBAC 矩阵）见 [`docs/architecture.md`](./docs/architecture.md)，需要时再读，不要常驻记忆。本节只保留做错会出事的禁令：

- 不要让 xray 加入 `internal_docker`（原因见 docs）
- XHTTP 模式必须是 `packet-up`，不要改成 `stream-up`
- 不要为 IPv4 入站加其他转发方式（宿主 iptables DNAT 等）
- 改 `services/admin/configs/` 下模板 = rebuild admin（模板与二进制原子对应）
- `vod_extractor` 部署硬前提（宿主机 cookies）见构建节注释

## Security — 多层防御

1. Cloudflare Authenticated Origin Pulls (mTLS): 只接受 CF Origin Pull 证书。证书: `cert/cloudflare-origin-pull.pem`
2. Go rate limiter: 20 req/10s per IP → 429 → 自动封禁 30min
3. Go IP blocker: 持久黑名单 → 403
4. fail2ban in nginx: 通过 scoped `X-Fail2Ban-Token` 调用 admin block/unblock API 封禁真实 IP
5. Cloudflare DDoS/WAF 在边缘
6. htpasswd bcrypt (`$2y$`): 所有密码用 bcrypt 哈希

"Never use `RemoteAddr`" 的原因就在这里——所有流量经 Docker bridge 内网地址（网段由 Docker 默认地址池动态分配），基于 RemoteAddr 的判断会全部失败。必须用 `X-Forwarded-User`（nginx 设置）或 `X-Internal-Token`（内部服务）。

## RBAC — 不要破坏这个

三层链：nginx basic auth → Go middleware chain → 前端路由守卫。

### 请求流

```
rateLimitMiddleware → ipBlockMiddleware → requireRole → handler
```

- `requireRole("admin")`: users.json 中 role=admin
- `requireRole("viewer")`: 任何已认证用户
- `X-Internal-Token` bypass: 仅保留给可信内部自动化的全权限共享 token；nginx/fail2ban 不挂载此 token
- `X-Fail2Ban-Token` scoped bypass: 只允许 fail2ban 调用 `POST /api/admin/security/block|unblock`

### 页面/API 访问矩阵

完整矩阵见 [`docs/architecture.md`](./docs/architecture.md#rbac-访问矩阵)。新增端点按最小角色选 `viewer`/`admin` 包裹。

## IPv6 寻址

寻址规则见 [`docs/architecture.md`](./docs/architecture.md#ipv6-寻址)。

## 关键约定

- 文档单一来源：架构"是什么"只写在 `docs/architecture.md`，AGENTS 不复述实现细节。文档与可验证实现冲突时以代码为准并修正文档，不要按错误描述继续。
- 配置字段核对: Xray、Nginx、Docker 等配置以官方源码/文档为准；优先读取官方 GitHub 仓库的 Go struct JSON tag 或官方文档确认字段名大小写、合法值范围和字段语义。
- Never use `RemoteAddr` for auth — 所有流量经 Docker bridge，看起来都是内网地址。
- Internal auth: `INTERNAL_TOKEN`（启动时自动生成，存储在 `.wework/.internal-token`）；fail2ban 使用独立 scoped token（`.wework/.fail2ban-token`）。
- Secrets: gitignored `configs/`、`cert/`、`.wework/`。本地 `.env` 覆盖默认值。
- Xray 客户端归属: 非 admin 用户只能管理自己的 Xray 客户端（每人一个）。Admin 可看全部。
- 配置文件写入：统一使用 `atomicWriteFile`（写 .tmp → rename），防止写到一半崩溃导致配置损坏。禁止直接 `os.WriteFile` 写配置文件。
- 错误响应：业务错误用 `writeError(w, msg, code)`（msg 返回客户端）；内部错误（文件 I/O、Docker API、第三方 API 等可能泄露内部状态的）必须用 `writeInternalError(w, err)`（只返回固定 "Internal error"，真实错误记日志）。
- 新增 API 端点：在 `main.go` 的 `mux.HandleFunc(...)` 区域注册，所有认证端点用 `auth.RequireRole("viewer")` 或 `auth.RequireRole("admin")` 包裹；健康检查用无认证的裸 handler。handler 实现放对应 internal/ 域包（platform←auth←accounts/proxy←media/infra/music/downloads/transfer，依赖单向无环）；大上传端点必须同步在 main.go 的 init() 里 `platform.ExemptBodyLimit(...)` 声明并在 main_test.go 契约测试登记。
- 环境变量：通过 `getEnv()` 读取（见 `env.go`），不直接用 `os.Getenv`。
- 镜像版本自报：admin/media_server/music_server/vod_extractor 的版本号在构建期烙进产物——Makefile 与 `deploy-changed.sh` 自动导出各仓库 `git describe`，compose 经 build args 传入（直接 `docker compose build` 不经 make/脚本时版本为 "dev"）。健康端点均携带版本：`/api/admin/health`、`/api/media/health`、`/api/music/health`、vod_extractor `/health`（含 yt-dlp 版本，YouTube 排障关键字段）；各镜像另有 OCI `org.opencontainers.image.revision` 标签可 `docker image inspect` 查询。
