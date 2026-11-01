# WorkWork

Docker-based proxy infrastructure with Xray VLESS proxy, Nginx reverse proxy, and a Go/Svelte admin panel.

## 架构概览

```
┌─────────────────────────────────────────────────────────────────┐
│                         Cloudflare                               │
│              (Full Proxy + WAF + DDoS Protection)                │
└─────────────────────────────┬───────────────────────────────────┘
                              │ :443
┌─────────────────────────────▼───────────────────────────────────┐
│                           Nginx                                  │
│                  (TLS termination + fail2ban)                   │
│                    │              │                             │
│            /admin/*          /proxy/query (XHTTP)                │
│            /api/admin/*          │                               │
│                    │              │                             │
│           ┌────────▼────────┐  ┌──▼────────┐                     │
│           │     Admin       │  │   Xray    │                     │
│           │  (Go + Svelte)  │  │  XHTTP    │                     │
│           │    :8318        │  │unix socket│                     │
│           └─────────────────┘  └───────────┘                     │
└─────────────────────────────────────────────────────────────────┘
```

### 核心组件

| 组件 | 技术栈 | 端口 | 说明 |
|------|--------|------|------|
| **nginx** | nginx:alpine + fail2ban | 443 | TLS 终结、反向代理、IP 防护 |
| **xray** | XTLS 官方源码构建 | 无入站端口（XHTTP 经 Unix socket 由 nginx `/proxy/query` 接入） | VLESS XHTTP 代理 |
| **admin** | Go + Svelte 5 | 8318 | 管理面板、用户管理、配置生成（docker API 经 **docker-proxy** 白名单代理） |
| **docker-proxy** | Go | 2375（容器内，仅内网） | docker API 精确白名单反代：只放行 admin 所需端点，create/delete/images/networks 等逃逸面一律 403 |

> media/music 已拆分至独立站：媒体中心（media_server + vod_extractor）在 [`media.chaoset.com`](https://media.chaoset.com)（仓库 `media_center`），在线音乐（music_server + netease_api）在 [`music.chaoset.com`](https://music.chaoset.com)（仓库 `music_player`，含 `deploy/` 自洽编排与 `netease_api/`）。旧前缀 `/api/media`、`/api/music` 的存量 App 由 CF 边缘动态重定向（308 → 各新站 `/api/*`）承载。

### 网络架构

- **ipvlan_docker** (external): IPv6 L3 出口，分配 `/64` 子网
- **internal_docker** (bridge): Docker 内部网络（网段由 Docker 默认地址池动态分配）
- **xray_egress** (bridge): Xray 专用 IPv4 egress/NAT 网络，不加入 admin 内网

完整服务说明见 [docs/architecture.md](./docs/architecture.md)。

## 快速开始

### 前置要求

- Docker + Docker Compose v2
- IPv6 支持（ipvlan 网络需要 parent interface）
- Cloudflare 账号（可选，用于 DNS 和代理）

### 启动服务

```bash
# 1. 配置环境变量（必须先填 IPV6_SUBNET：setup-networks.sh 会读取它）
cp .env.example .env
# 编辑 .env：IPV6_SUBNET 填你实际分配到的 /64（示例前缀 2001:db8::/32 是
# RFC 3849 文档地址，脚本会拒绝）；NGINX_IPV6/XRAY_IPV6 必须落在该 /64 内
chmod 600 .env

# 2. 创建 IPvlan 网络（首次运行，需 sudo）
sudo bash setup-networks.sh

# 3. 启动所有服务
docker compose up -d

# 4. 验证服务状态
docker compose ps
```

### 访问管理面板

- 地址: `https://your-domain.com/admin/`
- 首次启动如果没有既有 htpasswd，必须通过 `AUTH_USER` + `AUTH_PASS` 生成管理员 Basic Auth；后续用户在 Users 页面创建。

## 服务详情

### Xray 代理

| 协议 | 端口 | 用途 | 目标域名 |
|------|------|------|----------|
| **XHTTP** | 无独立端口（nginx `/proxy/query` 经 Unix socket 转发） | 通过 Cloudflare 连接 | `yinghuoren.com` |

客户端配置只包含 XHTTP 出站（经 Cloudflare + nginx TLS 终结接入）；内置并行 DNS 段加速代理域名解析。

### Admin 管理面板

提供以下功能：

| 页面 | 权限 | 说明 |
|------|------|------|
| Dashboard | viewer+ | 系统状态（admin）/ 个人信息（viewer） |
| Services | admin | Docker 容器管理（启停重启） |
| Logs | admin | 服务日志查看（实时流） |
| Clients | viewer+ | Xray 代理账号管理（一个用户可拥有多个客户端）、客户端配置下载 |
| Users | viewer+ | viewer 改自己密码；admin 用户增删改、角色分配 |
| Security | admin | IP 黑名单、限速状态管理 |
| Settings | admin | 网络状态、IPv6 轮换、DNS 同步 |
| Downloads | viewer+ | Claude Code / Xray 二进制下载 |
| Transfer | viewer+ | 临时文件传输（上传需登录，分享链接公开可下载） |

### Downloads 页面

支持下载 **Claude Code** 和 **Xray** 官方二进制文件：

| Tab | viewer 可见 | admin 可执行 |
|-----|-----------|-------------|
| Claude Code | 查看已下载、下载文件 | 刷新版本列表、下载新版本、删除 |
| Xray | 查看已下载、下载文件 | 刷新版本列表、下载新版本、删除 |

## 目录结构

```
workwork/
├── docker-compose.yml        # 生产环境 Docker Compose（4 服务）
├── Makefile                  # 构建/部署入口（make check / build / up / deploy-changed）
├── scripts/
│   ├── backup.sh
│   ├── compose-services.sh   # compose 服务清单唯一来源（Makefile/部署脚本共用）
│   ├── deploy-changed.sh     # 服务器部署：pull + 最小 build/up + 渲染产物内容比对
│   ├── env-schema.tsv        # 环境变量单一事实源（默认值/必填/校验/消费方）
│   ├── env-schema.sh         # 校验代码/compose/.env.example 与 schema 一致
│   ├── test-env-schema.sh    # 上述门禁的正/负例行为测试
│   └── restore-verify.sh     # 备份恢复机械校验：哈希/状态文件/sqlite 完整性/证书配对
├── .env                      # 环境变量（不提交）
├── .env.example              # 环境变量示例
├── setup-networks.sh         # 创建 ipvlan 网络
│
├── services/
│   ├── admin/                # Go 管理面板后端（internal/ 分域包,依赖单向无环）
│   │   ├── main.go          # 装配:路由注册、中间件链、后台任务启动、body-limit 豁免声明
│   │   ├── configs/         # nginx/xray 配置模板（唯一读者是 admin,构建期 COPY 进镜像）
│   │   ├── ui/              # Svelte 5 前端源码（构建产物打进 admin 镜像,非独立部署单元）
│   │   ├── deploy.go        # 启动引导:调用各域 Ensure* 生成运行时配置
│   │   ├── main_test.go     # 路由装配契约测试(大上传端点豁免清单)
│   │   └── internal/
│   │       ├── runtime/     # Layout(路径)、JSONFile[T](原子写/损坏隔离)、Clock
│   │       ├── platform/    # 零业务底座:env、HTTP 响应、docker 封装、compose 工具
│   │       ├── requestctx/  # 认证 actor 请求上下文(域包不依赖 auth)
│   │       ├── identity/    # users.json 唯一 owner、htpasswd/proxy 凭证、token、角色策略
│   │       ├── auth/        # 认证中间件、限流、IP 封禁、可信代理、nginx 封禁同步
│   │       ├── xray/        # 客户端台账、客户端配置生成、重启编排(渲染/重启注入)
│   │       ├── accounts/    # 用户管理 + Xray 客户端管理(删用户级联删客户端,故合域)
│   │       ├── proxy/       # IPv6 轮换、CF DNS、流量统计、服务守护 watcher
│   │       ├── infra/       # docker 服务管理、日志流、Dashboard
│   │       ├── nginxcfg/    # nginx 模板渲染(XHTTP 认证/端口/IPv6 注入)
│   │       ├── downloads/   # Claude Code / Xray 二进制下载
│   │       └── transfer/    # 文件中转(公开分享下载)
│   │
│   ├── xray/                # Xray 代理（运行时配置由 admin 渲染到 .wework/generated/）
│   │   ├── entrypoint.sh
│   │   └── Dockerfile
│   │
│   ├── docker_proxy/        # method+path 级 docker API 白名单反代（admin 经它操作容器）
│   │
│   ├── nginx/               # Nginx 反向代理（运行时配置由 admin 渲染）
│   │   ├── config/fail2ban/
│   │   └── Dockerfile
│   │
│
├── versions.env             # 基础镜像版本唯一来源(make check 门禁校验与 compose 兜底一致)
│
├── cert/                    # TLS 证书（不提交）
├── logs/                    # 日志输出
└── .wework/                # 运行时数据（不提交；generated/ 下为 admin 生成的配置）
```

## 版本排查

所有线上组件的版本号在构建期烙进产物：admin 经健康端点暴露 version；四个镜像都带
OCI `revision` 标签（xray/nginx 此前因 ARG 未在最终 stage 重新声明而为空，已修复）。

```bash
# 服务端健康端点(自带版本号)
curl -s -u <面板用户>:<密码> https://yinghuoren.com/api/admin/health    # admin 版本(经 nginx,/api/admin/ 有 basic auth)
docker compose exec nginx curl -s http://admin:8318/api/admin/health   # admin 版本

# 镜像构建源(OCI revision 标签，四个镜像都可查)
docker image inspect workwork-admin workwork-xray workwork-nginx workwork-docker-proxy \
  --format '{{.RepoTags}} {{index .Config.Labels "org.opencontainers.image.revision"}}'

# admin / docker-proxy 启动日志打印版本；xray/nginx 以镜像标签为准
docker compose logs admin docker-proxy | head
# media/music 各自独立站的版本排查见其仓库 deploy/README.md
```

## 备份与恢复

运行时状态（登录凭据、配置、临时文件、fail2ban 状态）都在单机 `.wework/` 与
`services/nginx/fail2ban-data/`；media/music 已拆分到独立仓库，其数据随各自仓库备份。
必须定期备份且异机存放：

```bash
make backup                    # 打包 .wework/ + cert/ + .env + fail2ban-data
BACKUP_RSYNC_TARGET=user@nas:/backups/workwork make backup   # 打包后异机 rsync（推荐，cron 配置见 docs/operations.md）
```

恢复步骤与每季度恢复演练清单见 [docs/operations.md](./docs/operations.md)，证书轮换
与 git 历史敏感信息处理也在该手册。

## 安全机制

### 多层防护

1. **Cloudflare Authenticated Origin Pulls (mTLS)**
   - 只允许 Cloudflare 服务器连接
   - CA 证书在镜像内 `/etc/nginx/cloudflare-origin-pull.pem`（构建期从 CF 官方下载）；
     不是仓库 `cert/` 下的文件（`cert/` 只放你自己的源站证书/私钥）

2. **Nginx Basic Auth**
   - `/admin/` 和 `/api/admin/` 需要 HTTP 基本认证
   - htpasswd 使用 bcrypt 哈希

3. **Go Rate Limiter**
   - 20 请求/10秒 per IP
   - 超限自动 ban 30 分钟

4. **fail2ban**
   - 监控 nginx auth log 中的 401
   - 调用 admin API 封禁真实客户端 IP

5. **RBAC 角色控制**
   - `admin`: 全部管理功能
   - `viewer`: 管理自己名下的 Xray 客户端（可多个）、下载客户端配置、改自己的密码

## 常用命令

```bash
# 推荐：根目录 Makefile（compose 薄封装）
make build admin nginx      # 构建镜像
make up admin               # 重建容器（--no-deps）
make restart admin          # 重启容器
make logs nginx             # 跟踪日志
make check                  # 本地门禁：Go/脚本/编排/版本/env schema/前端全量校验

# 等价的原始命令
docker compose build admin
docker compose up -d --no-deps admin
docker compose restart xray
docker compose logs -f xray
```

## 基础镜像版本统一（跨仓）

`versions.env` 是本仓基础镜像版本的唯一主本，同时统一并列站点仓
（`media_center` / `music_player` / `ops-portal` / `hotspot`）的版本：脚本扫描各仓的
versions.env、Dockerfile ARG 默认值、CI 的 go-version/bun-version、compose 兜底值、
`.env.example`/`.env.prod.example` 的版本对照值（活动行与 `# KEY=值` 注释行），
发现漂移即报错（已挂 `make check`）。

```bash
# 升级某个基础镜像版本（例：Go 1.26.9 → 1.26.10）：
vim versions.env                     # 只改主本一处
bash scripts/sync-versions.sh sync   # 把主本值刷写到各仓全部点位
# 然后各仓按 worktree 流程提交推送，服务器 pull 后 make build && make up
```

## 部署流程（先本地验证，再发布）

```bash
# 0. 本地验证（硬性前置，未通过禁止 push 与上线；完整门禁见 AGENTS.md「发布门禁」）
make check                  # 本地门禁：Go/脚本/编排/版本/env schema/前端全量校验
make build <受影响服务...>   # 受影响服务本地构建通过（版本号自动烙入）
# 本地能独立启动的服务：make up <svc...> 后等 healthy 并核对版本指纹（见「版本排查」）
# nginx / xray 全链路本地不具备启动条件（需 cert/ 与外部 ipvlan 网络），以构建通过为准

# 1. 本地开发（worktree 分支）合并进 main 并推送
git push origin main

# 2. 服务器一键部署（推荐）：pull + 按变更最小 build/up，
#    部署后自带健康自校验（等 healthy + 版本指纹核对，不通过即 fail 并给出回滚命令）。
#    配置消费者（xray/nginx）除模板变更外，还会比较渲染产物哈希：渲染代码或
#    相关 env 导致 generated/ 内容变化时同样会校验并重启，不依赖模板路径。
ssh workwork-server "cd workwork && make deploy-changed"
# 复跑/指定基线: scripts/deploy-changed.sh <old-commit>

# 3. 按变更选择最小部署命令（或直接用 make），例如：
make build admin && make up admin          # services/admin/**（含 ui 与配置模板）
make build nginx && make up nginx          # services/nginx/**
make build xray && make up xray                             # services/xray/**
```

## 环境变量

完整清单以 [.env.example](./.env.example) 与 [scripts/env-schema.tsv](./scripts/env-schema.tsv) 为准：
后者登记每个变量的默认值、是否必填、取值校验与消费方，`make check` 会校验它与
compose 的 `${KEY:-...}` 兜底、`.env.example` 三处一致，漂移即失败。本表只列最常改动的。

| 变量 | 说明 | 示例 |
|------|------|------|
| `COMPOSE_PROJECT_NAME` | docker-proxy 放行、admin 管理的 Compose 项目名（容器归属校验） | `workwork` |
| `AUTH_USER` / `AUTH_PASS` | 首启必需：初始管理员与密码（写入 htpasswd；面板改密后 `.env` 里的 `AUTH_PASS` 即作废） | - |
| `ADMIN_PORT` | admin 监听端口；compose healthcheck、nginx 反代与 fail2ban action 全链路派生 | `8318` |
| `NGINX_IPV6` | Nginx IPv6 地址（必须落在 `IPV6_SUBNET` 内） | `2605:xxx:xxx::2` |
| `XRAY_IPV6` | Xray IPv6 地址（必须落在 `IPV6_SUBNET` 内） | `2605:xxx:xxx::3` |
| `IPV6_SUBNET` | IPv6 /64 子网（`setup-networks.sh` 取首个 /80 作为 ip-range） | `2605:xxx:xxx::/64` |
| `CF_API_TOKEN` | Cloudflare API Token | - |
| `INTERNAL_TOKEN` | 内部服务通信令牌（会渲染进 nginx map，字符集限 `[A-Za-z0-9._~-]`） | 自动生成 |
| `INTERNAL_TOKEN_ALLOWED_PREFIXES` | 可选：internal token 的路径前缀白名单（逗号分隔）。配置后该 token 只能豁免这些前缀，其余路径退回正常认证，被拒请求以 `[security]` 留痕；留空保持全权 | `/api/admin/security,/api/admin/dashboard` |
| `TRUSTED_PROXY_CIDRS` | 允许设置 `X-Forwarded-User` 的代理 CIDR（默认仅 loopback） | `127.0.0.0/8,::1/128` |
| `TRUSTED_PROXY_HOSTS` | 允许设置 `X-Forwarded-User` 的代理主机名/IP（默认 Compose `nginx`） | `nginx` |
| `XHTTP_DOMAIN` | XHTTP 目标域名（兼 TLS SNI/证书名、nginx server_name） | `yinghuoren.com` |
| `XHTTP_ADDRESS` | 可选：客户端出站连接地址（优选入口域名/IP）。SNI 恒为 `XHTTP_DOMAIN`，留空 = 直连 `XHTTP_DOMAIN` | `opt.yinghuoren.com` |
| `XHTTP_ALPN` | 可选：客户端 ALPN。`h2`（默认，TCP 稳妥）/`h3`（QUIC/UDP，UDP 质量好的网络更快，生成配置会剔除 TCP 专属 sockopt） | `h2` |
| `TRANSFER_USER_QUOTA` | 文件中转每用户上传配额（字节，0 = 不限；默认 5GB） | `5368709120` |
| `WEBHOOK_URL` | 可选：关键事件通知（封禁/解封、限流自动封禁、xhttp 自愈达上限、部署校验失败、备份失败），POST JSON `{event, summary, details, time}`，失败只记日志。目标为 ops-portal 网关时配 `ALERT_MTLS_CERT/KEY` 持证推送（默认 `/webhook-caller/{cert,key}.pem`） | `https://notify.example.com/hook` |
