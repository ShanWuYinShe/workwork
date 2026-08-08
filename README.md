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
│           │    :8318        │  │   :8447   │  │   :443    │     │
│           └─────────────────┘  └───────────┘  └───────────┘     │
└─────────────────────────────────────────────────────────────────┘
```

### 核心组件

| 组件 | 技术栈 | 端口 | 说明 |
|------|--------|------|------|
| **nginx** | nginx:alpine + fail2ban | 443 | TLS 终结、反向代理、IP 防护 |
| **xray** | teddysun/xray | 443, 8447 | VLESS Reality + XHTTP 代理 |
| **admin** | Go + Svelte 5 | 8318 | 管理面板、用户管理、配置生成 |
| **media_server** | Go | 8787 | 合规公开视频聚合 API |
|
Media Center 源码通过 git submodule 引入到 [`services/media_center`](./services/media_center)（仓库 [`media_center`](https://github.com/ShanWuYinShe/media_center)），App 对接文档见 [`services/media_center/media_server/API.md`](./services/media_center/media_server/API.md)。首次克隆后需执行 `git submodule update --init --recursive`。

### 网络架构

- **ipvlan_docker** (external): IPv6 L3 出口，分配 `/64` 子网
- **internal_docker** (bridge): Docker 内部网络，`172.20.0.0/16`
- **xray_egress** (bridge): Xray 专用 IPv4 egress/NAT 网络，不加入 admin/media 内网

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
| **XHTTP** | 8447 | 通过 Cloudflare 连接 | `yinghuoren.com` |

客户端配置包含 Reality + XHTTP 两种出站协议，自动选择延迟最低的线路。

### Admin 管理面板

提供以下功能：

| 页面 | 权限 | 说明 |
|------|------|------|
| Dashboard | viewer+ | 系统状态（admin）/ 个人信息（viewer） |
| Services | admin | Docker 容器管理（启停重启） |
| Clients | viewer+ | Xray 代理账号管理、客户端配置下载 |
| Users | admin | 用户增删改、角色分配 |
| Security | admin | IP 黑名单、限速状态管理 |
| Settings | admin | 网络状态、IPv6 轮换、DNS 同步 |
| Media Center | admin | 媒体服务 upstream、nginx 公开前缀、分类搜索词和过滤词配置 |
| Downloads | viewer+ | Claude Code / Xray 二进制下载 |

### Downloads 页面

支持下载 **Claude Code** 和 **Xray** 官方二进制文件：

| Tab | viewer 可见 | admin 可执行 |
|-----|-----------|-------------|
| Claude Code | 查看已下载、下载文件 | 刷新版本列表、下载新版本、删除 |
| Xray | 查看已下载、下载文件 | 刷新版本列表、下载新版本、删除 |

## 目录结构

```
workwork/
├── docker-compose.yml        # 生产环境 Docker Compose
├── .env                      # 环境变量（不提交）
├── .env.example              # 环境变量示例
├── setup-networks.sh        # 创建 ipvlan 网络
│
├── services/
│   ├── admin/                # Go 管理面板后端
│   │   ├── main.go          # HTTP 路由、中间件链
│   │   ├── api.go          # 核心 API（用户、服务、网络）
│   │   ├── middleware.go   # 认证、角色检查
│   │   ├── xray.go         # Xray 下载管理
│   │   ├── xray_clients.go # 客户端配置生成
│   │   ├── deploy.go       # 启动时配置生成
│   │   ├── cloudflare.go   # Cloudflare DNS
│   │   ├── claude.go       # Claude Code 下载
│   │   └── handlers/       # 请求处理器
│   │
│   ├── xray/                # Xray 代理配置
│   │   ├── config.json.tmpl
│   │   ├── entrypoint.sh
│   │   └── Dockerfile
│   │
│   ├── nginx/               # Nginx 反向代理
│   │   ├── nginx.conf.tmpl
│   │   ├── fail2ban/
│   │   └── Dockerfile
│   │
│   ├── media_center/        # Media Center 子模块
│   │   ├── media_app/       # Android TV 客户端
│   │   ├── media_server/    # Go 聚合后端
│
├── ui/                      # Svelte 5 管理面板前端
│   └── src/routes/          # 各页面组件
│
├── cert/                    # TLS 证书（不提交）
├── logs/                    # 日志输出
└── .wework/                # 运行时数据（不提交）
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
# 按服务构建并重建（通常不要加 --no-cache）
docker compose build admin
docker compose up -d --no-deps admin

# 查看日志
docker compose logs -f admin
docker compose logs -f nginx
docker compose logs -f xray
docker compose logs -f media_server
# 重启服务
docker compose restart admin
docker compose restart xray
docker compose restart media_server
```

## 部署流程

```bash
# 1. 本地开发并提交
git add .
git commit -m "描述"
git push origin main

# 2. 服务器拉取并查看变更
ssh workwork-server "cd service && old=\$(git rev-parse HEAD) && git pull --ff-only origin main && git diff --name-only \$old HEAD"

# 3. 按变更选择最小部署命令，例如：
# services/admin/** 或 ui/**
docker compose build admin && docker compose up -d --no-deps admin
# services/nginx/**
docker compose build nginx && docker compose up -d --no-deps nginx
# services/xray/**
docker compose build xray && docker compose up -d --no-deps xray
# services/media_center/media_server/**
docker compose build media_server && docker compose up -d --no-deps media_server
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
