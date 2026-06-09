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
| **media_server** | Node.js + TypeScript | 8787 | 合规公开视频聚合 API |
| **vod_extractor** | Python + yt-dlp | 8790 | 内部点播解析服务 |

Media Server 的 App 对接文档见 [`services/media_server/API.md`](./services/media_server/API.md)，OpenAPI 见 [`services/media_server/openapi.json`](./services/media_server/openapi.json)。

### 网络架构

- **ipvlan_docker** (external): IPv6 L3 出口，分配 `/64` 子网
- **internal_docker** (bridge): Docker 内部网络，`172.20.0.0/16`

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
service/
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
│   ├── media_server/        # Media Center 后端服务
│   │   ├── src/
│   │   ├── test/
│   │   └── Dockerfile
│   │
│   ├── vod_extractor/       # 内部 yt-dlp 点播解析服务
│   │   ├── server.py
│   │   └── Dockerfile
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
# 构建并启动
docker compose build --no-cache admin nginx media_server vod_extractor
docker compose up -d

# 查看日志
docker compose logs -f admin
docker compose logs -f nginx
docker compose logs -f xray
docker compose logs -f media_server
docker compose logs -f vod_extractor

# 重启服务
docker compose restart admin
docker compose restart xray
docker compose restart media_server
docker compose restart vod_extractor
```

## 部署流程

```bash
# 1. 本地开发并提交
git add .
git commit -m "描述"
git push origin main

# 2. 服务器拉取并部署
ssh workwork-server "cd service && git pull origin main && docker compose build --no-cache admin nginx media_server vod_extractor && docker compose up -d"
```

## 环境变量

| 变量 | 说明 | 示例 |
|------|------|------|
| `NGINX_IPV6` | Nginx IPv6 地址 | `2605:xxx:xxx:1::1` |
| `XRAY_IPV6` | Xray IPv6 地址 | `2605:xxx:xxx:1::2` |
| `IPV6_SUBNET` | IPv6 子网 | `2605:xxx:xxx::/64` |
| `CF_API_TOKEN` | Cloudflare API Token | - |
| `INTERNAL_TOKEN` | 内部服务通信令牌 | 自动生成 |
| `XRAY_REALITY_DOMAIN` | Reality 目标域名 | `xray.yinghuoren.com` |
| `XHTTP_DOMAIN` | XHTTP 目标域名 | `yinghuoren.com` |
| `MEDIA_CENTER_UPSTREAM` | nginx 转发到媒体服务的内部 upstream | `media_server:8787` |
| `MEDIA_CENTER_PUBLIC_PATH` | 媒体 API 线上公开前缀 | `/api/media` |
| `MEDIA_ASSET_SIGNING_KEY` | 媒体 `/asset` 代理 URL 签名密钥（留空则进程内随机） | - |
| `CONTENT_CATEGORIES_FILE` | Media Server 运行时来源和分类配置（compose 内由 admin 生成） | `/config/contentCategories.json` |
| `VOD_EXTRACTOR_TIMEOUT_MS` | 内部点播解析服务超时 | `30000` |
| `VOD_EXTRACTOR_MAX_CONCURRENT` | 内部点播解析最大并发进程数 | `2` |