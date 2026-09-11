# workwork

Docker 代理基础设施：Xray VLESS Reality + XHTTP 代理 / Nginx 反代 / Go 管理面板。

通用约束（Git 规范、Commit 格式、验证流程、工具链偏好、Docker 等）以全局 `~/AGENTS.md` 为准（各 AI 工具的全局指令文件均链接到该文件），本文件只做项目特有补充，不得放宽或违背全局约束。

AI Agent 工作目录：所有 AI agent 工作期间产生的中间文件写入项目根 `.workwork/`（已 `.gitignore`）。

## 硬约束

所有服务功能必须隔离在 Docker 容器内。 禁止修改宿主机环境设置（iptables、sysctl、网络配置），创建 Docker 网络除外。IPv4 路由走 Docker bridge NAT；IPv6 路由走 ipvlan L3。admin 服务通过 `docker.sock` 和 `containerExec()` 操作其他容器。

## 构建 & 部署

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

# Svelte 前端构建（使用 Bun；Docker 多阶段构建用 oven/bun 镜像 + bun.lock 冻结锁文件）
bun install --cwd ui                   # 安装依赖（生成/校验 ui/bun.lock）
bun run --cwd ui build                 # 构建

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
# 原则：先判断 git pull 带来的文件变更，只 build / recreate 受影响服务；
# 禁止无脑 --no-cache 全量 build 或 docker compose up -d 全量重建。
#
# 权限注意：
#   .wework/ 和 logs/ 必须归 uid=1001 (workuser) 所有（admin 以 user:1001:1001 运行）
#   但 logs/nginx/ 必须归 root（nginx master 以 root 写日志，cap_drop:ALL 无 DAC_OVERRIDE）
#   首次部署或权限异常时：
#     sudo chown -R 1001:1001 .wework/ logs/ && sudo chown -R root:root logs/nginx/
ssh workwork-server "cd service && old=\$(git rev-parse HEAD) && git pull --ff-only origin main && git diff --name-only \$old HEAD"

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
#       workwork-server:service/.wework/generated/media_center/media-center-app-v<vc>-<sha12>.apk
#     # legacy 变体（Android 4.4.2 / API 19）:
#     JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home" \
#       services/media_center/media_app_legacy/gradlew -p services/media_center/media_app_legacy :app:assembleRelease --no-daemon && \
#     scp services/media_center/media_app_legacy/app/build/outputs/apk/release/app-release.apk \
#       workwork-server:service/.wework/generated/media_center/media-center-app-legacy-v<vc>-<sha12>.apk
#     # 上传后手写 app-update.json / app-update-legacy.json（含 versionCode/versionName/
#     # apkFilename/sizeBytes/sha256/releaseNotes），或在管理面板“Android TV App 更新发布”
#     # 页面选变体后上传（admin 自动写 manifest + APK）。
# - services/media_center/media_server/**（git submodule）:
#     docker compose build media_server && docker compose up -d --no-deps media_server
# - services/media_center/vod_extractor/**（git submodule；YouTube yt-dlp 解析。
#     播放硬前提：宿主机 .wework/vod_extractor/youtube-cookies.txt 必须存在
#     （Netscape 格式 cookies，过期需重新导出），否则解析全被 bot 检测拦截）:
#     docker compose build vod_extractor && docker compose up -d --no-deps vod_extractor
#     （搜索还需 .env 配 YOUTUBE_API_KEY；解析服务自身一般无需改动）
# - services/music_player/music_server/**（git submodule；曲库在宿主机
#     .wework/music-library/，管理面板「音乐曲库」页上传或手动投放，扫描
#     TTL 内生效；网络源上游失败自动降级仅本地）:
#     docker compose build music_server && docker compose up -d --no-deps music_server
# - services/netease_api/** 或 music_server 需要网络源时:
#     docker compose build netease_api music_server && \
#     docker compose up -d --no-deps netease_api music_server
#     （可选 .env 配 NETEASE_MUSIC_COOKIE 解锁登录态曲目；匿名仅免费曲）
# - services/admin/** 或 ui/**:
#     docker compose build admin && docker compose up -d --no-deps admin
# - services/nginx/**:
#     docker compose build nginx && docker compose up -d --no-deps nginx
# - services/xray/**:
#     docker compose build xray xray-forward && docker compose up -d --no-deps xray xray-forward
# - docker-compose.yml / 共享网络 / 共享 env 变更:
#     docker compose up -d <受影响服务...>
# admin 的 deploy.go / 模板改动可能影响运行时生成的 nginx/xray/media 配置；
# admin 重启后只重启实际配置变化的下游服务。
```

## 架构

### services/admin/ — Go 管理面板（端口 8318，内部）

HTTP 服务，涵盖 Dashboard / 用户管理 / 网络配置 / Xray 客户端 CRUD / 安全封禁 / Cloudflare DNS / 部署配置生成。单容器多阶段构建（bun → golang → alpine），通过 `docker.sock` 操作其他容器。

### ui/ — Svelte 5 SPA

Vite 构建，由 admin Go 二进制从 `/app/ui` 托管（多阶段 Dockerfile: oven/bun → golang → alpine）。

### services/xray/ — Xray VLESS 代理

- 单容器双网络：IPv4 出站 via 专用 `xray_egress` bridge NAT；IPv6 via ipvlan L3。**IPv4 入站 443 由 `xray-forward` 转发容器发布**（纯 bridge + socat，`0.0.0.0:443:443` → `xray:443`），xray 本身不声明 ports。原因：xray 容器连接 ipvlan 网络后，Docker(29.x) 会把 ipvlan 选为默认网关网络(gwep4)，该容器所有端口映射整体失效（ipvlan 驱动不支持端口映射，与网络连接顺序无关，已实测）。不要让 xray 加入 `internal_docker`
- Inbounds: `xhttp-in`（8447，经 Cloudflare）+ `reality-in`（443，Reality 直连）
- XHTTP 模式必须是 `packet-up`——上行数据拆为独立 POST 逐包发送，适合不稳定网络；不要改成 `stream-up`
- Clients 用 `email` 字段（= 用户名）做 per-user 策略/流量统计

### services/nginx/ — Nginx + fail2ban

托管 admin SPA，反代 `/api/admin/` → admin:8318，`/proxy/query` → xray Unix socket。fail2ban 通过 scoped `X-Fail2Ban-Token` 调用 admin block/unblock API 封禁真实客户端 IP。

### services/media_center/media_server/ — Media Center 目录服务（git submodule）

目录服务：Go HTTP，从 catalog(SQLite) 提供 provider 列表、分类列表、视频元数据。播放按 provider 分发：bilibili 由 App 端 WBI 签名本地解析；youtube 由 server 端两个受控实时端点处理（`/api/providers/youtube/search` 走 Data API 搜索、`/api/providers/youtube/resolve` 代理 `vod_extractor` 解析播放直链，均要求 X-App-Token + 独立限流，见 `media_server/youtube.go`）。不做 asset 代理。源码以 git submodule 形式引入自 `media_center` 仓库（`git submodule update --init --recursive` 拉取）。线上由 admin 生成运行时分类配置，nginx 默认通过 `/api/media` 转发到 `media_server:8787`。

### docker-compose.yml

6 服务: admin, xray, xray-forward, nginx, media_server, vod_extractor。网络: `ipvlan_docker`（外部，IPv6 L3）+ `internal_docker`（admin/media/vod_extractor 内网 bridge，172.20.0.0/16）+ `xray_egress`（xray 专用 IPv4 egress bridge）。`xray` 持有公网 IPv6（ipvlan L3 直连），IPv4 直连 443 由 `xray-forward`（bridge-only socat 转发容器）发布，转发目标 `xray:443`（xray_egress 网络内）。不要为 IPv4 再加其他转发方式（宿主 iptables DNAT 等）——Docker 管理的端口映射已足够。
vod_extractor 仅内网（媒体中心 YouTube 解析）；部署硬前提：宿主机 `.wework/vod_extractor/youtube-cookies.txt`（YouTube cookies，Netscape 格式），缺失时 YouTube 解析全被 bot 检测拦截；实时搜索需 `.env` 配 `YOUTUBE_API_KEY`。

## Security — 多层防御

1. Cloudflare Authenticated Origin Pulls (mTLS): 只接受 CF Origin Pull 证书。证书: `cert/cloudflare-origin-pull.pem`
2. Go rate limiter: 20 req/10s per IP → 429 → 自动封禁 30min
3. Go IP blocker: 持久黑名单 → 403
4. fail2ban in nginx: 通过 `INTERNAL_TOKEN` 调用 admin API 封禁真实 IP
5. Cloudflare DDoS/WAF 在边缘
6. htpasswd bcrypt (`$2y$`): 所有密码用 bcrypt 哈希

"Never use `RemoteAddr`" 的原因就在这里——所有流量经 Docker bridge (172.20.x.x)，基于 RemoteAddr 的判断会全部失败。必须用 `X-Forwarded-User`（nginx 设置）或 `X-Internal-Token`（内部服务）。

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

| 页面 | API 端点 | 角色 |
|------|---------|------|
| Dashboard | `GET /api/admin/dashboard` | viewer+（admin 看统计，viewer 看个人数据） |
| Services（含 Logs） | `GET/POST /api/admin/services/*` | admin |
| Clients | `GET/POST /api/admin/users/xray/*` | viewer+（仅自己的客户端） |
| Users 管理 | `POST /api/admin/users/admin/*`, `/roles/set` | admin |
| 账户 | `GET /api/admin/auth/me`, `GET /api/admin/users`, `POST /api/admin/users/password` | viewer+ |
| Security | `GET/POST /api/admin/security/*` | admin |
| Settings | `GET/POST /api/admin/network/*` | admin |
| Media Center | `GET/PUT /api/admin/media-center/config`, `/credentials`; `GET/POST /api/admin/media-center/catalog/*` | admin |
| App 版本管理 | `GET/POST /api/admin/media-center/app-release`；`GET .../app-release/history`、`GET .../app-release/download`、`POST .../app-release/rollback`、`DELETE .../app-release/history`（发布历史 / 回滚 / 删除历史版本 / 按版本下载 APK） | admin |
| Downloads | `GET /api/admin/claude-code/stored`, `/downloads/*` | viewer+ |
| Downloads 管理 | `GET/POST/DELETE /api/admin/claude-code/*` | admin |
| Xray 二进制 | `GET /api/admin/xray/stored`, `/downloads/*` | viewer+ |
| Xray 管理 | `GET/POST/DELETE /api/admin/xray/*` | admin |
| Transfer | `POST/GET/DELETE /api/admin/transfer/*` | viewer+ |
| Transfer 公开下载 | `GET /api/transfer/{code}` | 无需认证 |

*Downloads 页面对所有用户可见，但 admin-only 的操作（download/delete）只对 admin 展示。

## IPv6 寻址

- 子网: `/64` from `IPV6_SUBNET` env var
- 静态范围: `{prefix}:1::/80` — Docker 分配（nginx、xray 主 IP）
- 轮换范围: `{prefix}:f::/80` — 第 5 个 hextet = `000f`，DNS 指向此处
- `xray.yinghuoren.com` → 轮换 IP（reality 用域名，非裸 IP）
- 客户端配置: reality 出站用 `xray.yinghuoren.com`，xhttp 用 `yinghuoren.com`
- 不需要宿主机 sysctl 配置：ipvlan L3 模式下容器直接持有公网 IPv6，数据包在内核驱动层直接投递到容器命名空间。无需启用 `net.ipv6.conf.all.forwarding`、`proxy_ndp` 等宿主机网络设置。禁止修改宿主机 sysctl/iptables/网络配置。

## 关键约定

- 配置字段核对: Xray、Nginx、Docker 等配置以官方源码/文档为准；优先读取官方 GitHub 仓库的 Go struct JSON tag 或官方文档确认字段名大小写、合法值范围和字段语义。
- Never use `RemoteAddr` for auth — 所有流量经 Docker bridge，看起来都是内网地址。
- Internal auth: `INTERNAL_TOKEN`（启动时自动生成，存储在 `.wework/.internal-token`）；fail2ban 使用独立 scoped token（`.wework/.fail2ban-token`）。
- Secrets: gitignored `configs/`、`cert/`、`.wework/`。本地 `.env` 覆盖默认值。
- Xray 客户端归属: 非 admin 用户只能管理自己的 Xray 客户端（每人一个）。Admin 可看全部。
- 配置文件写入：统一使用 `atomicWriteFile`（写 .tmp → rename），防止写到一半崩溃导致配置损坏。禁止直接 `os.WriteFile` 写配置文件。
- 错误响应：业务错误用 `writeError(w, msg, code)`（msg 返回客户端）；内部错误（文件 I/O、Docker API、第三方 API 等可能泄露内部状态的）必须用 `writeInternalError(w, err)`（只返回固定 "Internal error"，真实错误记日志）。
- 新增 API 端点：在 `main.go` 的 `mux.HandleFunc(...)` 区域注册，所有认证端点用 `requireRole("viewer")` 或 `requireRole("admin")` 包裹；健康检查用无认证的裸 handler。
- 环境变量：通过 `getEnv()` 读取（见 `env.go`），不直接用 `os.Getenv`。
