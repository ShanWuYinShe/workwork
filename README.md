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
│                    │              │              │                 │
│            /admin/*          /api/admin/*      /proxy/query      │
│                    │              │              │                 │
│           ┌────────▼────────┐  ┌──▼────────┐  ┌──▼────────┐     │
│           │     Admin       │  │   Xray    │  │  Xray     │     │
│           │  (Go + Svelte)  │  │  XHTTP    │  │  Reality  │     │
│           │    :8318        │  │unix socket│  │   :443    │     │
│           └─────────────────┘  └───────────┘  └───────────┘     │
└─────────────────────────────────────────────────────────────────┘
```

### 核心组件

| 组件 | 技术栈 | 端口 | 说明 |
|------|--------|------|------|
| **nginx** | nginx:alpine + fail2ban | 443 | TLS 终结、反向代理、IP 防护 |
| **xray** | XTLS 官方源码构建 | 443（Reality）；XHTTP 无独立端口（Unix socket 经 nginx `/proxy/query` 接入） | VLESS Reality + XHTTP 代理；IPv4 直连 443 由 **xray-forward** 转发容器发布 |
| **admin** | Go + Svelte 5 | 8318 | 管理面板、用户管理、配置生成 |
| **media_server** | Go | 8787 | 合规公开视频聚合 API |
| **vod_extractor** | Python (yt-dlp) | 8790 | YouTube 搜索/播放解析（仅内网，media_server 调用；需宿主机 cookies） |
| **music_server** | Go | 8790 | 在线音乐 API（本地曲库 + 网易云网络源聚合，服务端代理流） |
| **netease_api** | Node(Bun) | 3000 | 网易云 API 上游（仅内网，music_server 调用） |
Media Center 源码通过 git submodule 引入到 [`services/media_center`](./services/media_center)（仓库 [`media_center`](https://github.com/ShanWuYinShe/media_center)），App 对接文档见 [`services/media_center/media_server/API.md`](./services/media_center/media_server/API.md)。Music Player 源码通过 submodule 引入到 [`services/music_player`](./services/music_player)（仓库 [`music_player`](https://github.com/ShanWuYinShe/music_player)，HarmonyOS 客户端 `music_app/` + Go 服务端 `music_server/`，组织方式同 media_center）。首次克隆后需执行 `git submodule update --init --recursive`。

### 网络架构

- **ipvlan_docker** (external): IPv6 L3 出口，分配 `/64` 子网
- **internal_docker** (bridge): Docker 内部网络（网段由 Docker 默认地址池动态分配）
- **xray_egress** (bridge): Xray 专用 IPv4 egress/NAT 网络，不加入 admin/media 内网

完整服务说明见 [docs/architecture.md](./docs/architecture.md)。

## 快速开始

### 前置要求

- Docker + Docker Compose v2
- IPv6 支持（ipvlan 网络需要 parent interface）
- Cloudflare 账号（可选，用于 DNS 和代理）

### 启动服务

```bash
# 1. 创建 IPvlan 网络（首次运行，需 sudo）
sudo bash setup-networks.sh

# 2. 配置环境变量
cp .env.example .env
# 编辑 .env 填写必要的配置
chmod 600 .env

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

支持两种入站协议：

| 协议 | 端口 | 用途 | 目标域名 |
|------|------|------|----------|
| **Reality** | 443 | 直接连接（抗审查） | `xray.yinghuoren.com` |
| **XHTTP** | 无独立端口（nginx `/proxy/query` 经 Unix socket 转发） | 通过 Cloudflare 连接 | `yinghuoren.com` |

客户端配置包含 Reality + XHTTP 两种出站协议，observatory 探测 + leastLoad 均衡自动选择延迟最低的线路；另内置并行 DNS 段加速代理域名解析。

### Admin 管理面板

提供以下功能：

| 页面 | 权限 | 说明 |
|------|------|------|
| Dashboard | viewer+ | 系统状态（admin）/ 个人信息（viewer） |
| Services | admin | Docker 容器管理（启停重启） |
| Logs | admin | 服务日志查看（实时流） |
| Clients | viewer+ | Xray 代理账号管理、客户端配置下载 |
| Users | admin | 用户增删改、角色分配 |
| Security | admin | IP 黑名单、限速状态管理 |
| Settings | admin | 网络状态、IPv6 轮换、DNS 同步 |
| Media Center | admin | 媒体服务 upstream、nginx 公开前缀、分类搜索词和过滤词配置 |
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
├── docker-compose.yml        # 生产环境 Docker Compose（8 服务）
├── Makefile                  # 构建/部署入口（make check / build / up / deploy-changed）
├── scripts/
│   └── deploy-changed.sh     # 服务器部署：pull + 子模块同步 + 按变更最小 build/up
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
│   │       ├── platform/    # 零业务底座:env、原子写、HTTP 响应、docker 封装、compose 工具
│   │       ├── auth/        # 认证原语:htpasswd、用户元数据、token、中间件链、限流、IP 封禁
│   │       ├── accounts/    # 用户管理 + Xray 客户端管理(删用户级联删客户端,故合域)
│   │       ├── proxy/       # xray/nginx 配置路径、IPv6 轮换、CF DNS、服务守护 watcher
│   │       ├── infra/       # docker 服务管理、日志流、Dashboard
│   │       ├── media/       # 媒体中心配置、目录审核、APK 发布、nginx 接线渲染
│   │       ├── music/       # 曲库、分片上传、网易云扫码登录
│   │       ├── downloads/   # Claude Code / Xray 二进制下载
│   │       └── transfer/    # 文件中转(公开分享下载)
│   │
│   ├── xray/                # Xray 代理（运行时配置由 admin 渲染到 .wework/generated/）
│   │   ├── entrypoint.sh
│   │   ├── Dockerfile / Dockerfile.forward   # xray 主容器 / IPv4 443 转发容器
│   │
│   ├── nginx/               # Nginx 反向代理（运行时配置由 admin 渲染）
│   │   ├── config/fail2ban/
│   │   └── Dockerfile
│   │
│   ├── media_center/        # git submodule（独立产品仓库）
│   │   ├── media_app/       # Android TV 客户端（版本号由子仓 git tag 驱动）
│   │   ├── media_app_legacy/ # Android 4.4.2 兼容变体
│   │   ├── media_server/    # Go 聚合后端
│   │   └── vod_extractor/   # YouTube yt-dlp 解析服务
│   │
│   ├── music_player/        # git submodule（独立产品仓库）
│   │   ├── music_app/       # HarmonyOS 客户端
│   │   ├── music_server/    # Go 在线音乐服务(本地曲库+网易网络源)
│   │   ├── tools/           # 子仓内部工具
│   │   └── shader_playground/ # 子仓实验项目
│   │
│   └── netease_api/         # 网易云 API 上游(Bun 运行 npm 包)
│
├── cert/                    # TLS 证书（不提交）
├── logs/                    # 日志输出
└── .wework/                # 运行时数据（不提交；generated/ 下为 admin 生成的配置）
```

## 版本排查

所有线上组件的版本号在构建期烙进产物，健康端点与镜像标签均可查：

```bash
# 服务端健康端点(自带版本号)
curl -s https://yinghuoren.com/api/media/health   # {"status":"ok","version":"v1.7.0"}
curl -s https://yinghuoren.com/api/music/health   # music_server 版本
docker compose exec nginx curl -s http://admin:8318/api/admin/health   # admin 版本
curl -s http://内网地址:8790/health                # vod_extractor(含 yt-dlp 版本)

# 镜像构建源(OCI revision 标签)
docker image inspect workwork-media_server --format '{{index .Config.Labels "org.opencontainers.image.revision"}}'

# 服务启动日志首行均打印版本
docker compose logs admin media_server music_server vod_extractor | head
```

## 安全机制

### 多层防护

1. **Cloudflare Authenticated Origin Pulls (mTLS)**
   - 只允许 Cloudflare 服务器连接
   - 证书: `cert/cloudflare-origin-pull.pem`

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
   - `viewer`: 查看服务状态、管理自己的 Xray 账号、下载客户端配置

## 常用命令

```bash
# 推荐：根目录 Makefile（compose 薄封装）
make build admin nginx      # 构建镜像
make up admin               # 重建容器（--no-deps）
make restart admin          # 重启容器
make logs nginx             # 跟踪日志
make check                  # 本地门禁：gofmt/vet/test + 前端构建

# 等价的原始命令
docker compose build admin
docker compose up -d --no-deps admin
docker compose restart xray
docker compose logs -f media_server
```

## 部署流程

```bash
# 1. 本地开发（worktree 分支）合并进 main 并推送
git push origin main

# 2. 服务器一键部署（推荐）：pull + 子模块同步 + 按变更最小 build/up
ssh workwork-server "cd workwork && make deploy-changed"
# 复跑/指定基线: scripts/deploy-changed.sh <old-commit>

# 2'. 手动部署时务必同步子模块（git pull 不会更新子模块指针，
#     跳过这步会用旧子模块源码构建而不自知）：
ssh workwork-server "cd workwork && old=\$(git rev-parse HEAD) && git pull --ff-only origin main && git submodule update --init --recursive && git diff --name-only \$old HEAD && git submodule status"

# 3. 按变更选择最小部署命令（或直接用 make），例如：
make build admin && make up admin          # services/admin/**（含 ui 与配置模板）
make build nginx && make up nginx          # services/nginx/**
make build xray xray-forward && make up xray xray-forward   # services/xray/**
make build media_server && make up media_server             # services/media_center/media_server/**
```

## 环境变量

| 变量 | 说明 | 示例 |
|------|------|------|
| `COMPOSE_PROJECT_NAME` | admin 通过 docker.sock 管理的 Compose 项目名 | `workwork` |
| `NGINX_IPV6` | Nginx IPv6 地址 | `2605:xxx:xxx:1::1` |
| `XRAY_IPV6` | Xray IPv6 地址 | `2605:xxx:xxx:1::2` |
| `IPV6_SUBNET` | IPv6 子网 | `2605:xxx:xxx::/64` |
| `CF_API_TOKEN` | Cloudflare API Token | - |
| `INTERNAL_TOKEN` | 内部服务通信令牌 | 自动生成 |
| `TRUSTED_PROXY_CIDRS` | 允许设置 `X-Forwarded-User` 的代理 CIDR（默认仅 loopback） | `127.0.0.0/8,::1/128` |
| `TRUSTED_PROXY_HOSTS` | 允许设置 `X-Forwarded-User` 的代理主机名/IP（默认 Compose `nginx`） | `nginx` |
| `XRAY_REALITY_DOMAIN` | Reality 目标域名 | `xray.yinghuoren.com` |
| `XHTTP_DOMAIN` | XHTTP 目标域名 | `yinghuoren.com` |
| `MEDIA_CENTER_UPSTREAM` | nginx 转发到媒体服务的内部 upstream | `media_server:8787` |
| `MEDIA_CENTER_PUBLIC_PATH` | 媒体 API 线上公开前缀 | `/api/media` |
| `CONTENT_CATEGORIES_FILE` | Media Server 运行时来源和分类配置（compose 内由 admin 生成） | `/config/contentCategories.json` |
| `MEDIA_SERVER_PUBLIC_URL` | 媒体 API 公网基址，固定用于拼接 App 更新下载地址（防 Host 投毒，需含 `/api/media` 前缀） | `https://yinghuoren.com/api/media` |
| `MEDIA_SERVER_APP_API_TOKEN` | 凭证接口令牌，与 App 构建时注入的 `APP_API_TOKEN` 一致；为空则凭证接口 401 | `openssl rand -hex 32` |
| `MEDIA_SERVER_ALLOWED_HOSTS` | 未配置 PUBLIC_URL 时允许推导下载地址的公网域名白名单（逗号分隔） | `yinghuoren.com` |
| `TRANSFER_USER_QUOTA` | 文件中转每用户上传配额（字节，0 = 不限；默认 5GB） | `5368709120` |
