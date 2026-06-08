# CLAUDE.md

Docker 代理基础设施：Xray VLESS Reality + XHTTP 代理 / Nginx 反代 / Go 管理面板。

通用约束（Git 规范、验证流程、工具链偏好）见全局 `~/.claude/CLAUDE.md`。

## 硬约束

**所有服务功能必须隔离在 Docker 容器内。** 禁止修改宿主机环境设置（iptables、sysctl、网络配置），创建 Docker 网络除外。IPv4 路由走 Docker bridge NAT；IPv6 路由走 ipvlan L3。admin 服务通过 `docker.sock` 和 `containerExec()` 操作其他容器。

## 构建 & 部署

```bash
# Go 编译检查
go build -C services/admin -o /dev/null .

# Go 验证
go vet -C services/admin ./...         # 静态检查
gofmt -l services/admin/               # 格式化检查（列出不符合的文件）
go test -C services/admin ./...        # 单元测试

# Svelte 前端构建（因 Docker 多阶段构建 + 远程部署环境限制，此处使用 npm 而非 bun）
npm --prefix ui run build              # 构建

# 部署（git push 后在服务器执行；用户明确要求时，Copilot/Claude 可执行 SSH 部署）
# 注意顺序：build → image prune 清旧 latest → volume prune 清孤儿 volume → up
# 这样 docker 相关文件不会持续膨胀
#
# 权限注意：
#   .wework/ 和 logs/ 必须归 uid=1001 (workuser) 所有（admin 以 user:1001:1001 运行）
#   但 logs/nginx/ 必须归 root（nginx master 以 root 写日志，cap_drop:ALL 无 DAC_OVERRIDE）
#   首次部署或权限异常时：
#     sudo chown -R 1001:1001 .wework/ logs/ && sudo chown -R root:root logs/nginx/
ssh workwork-server "cd service && git pull origin main && docker compose build --no-cache admin nginx && docker image prune -f && docker volume prune -f && docker compose up -d"
```

## 架构

### services/admin/ — Go 管理面板（端口 8318，内部）

| 文件 | 职责 |
|------|------|
| main.go | 路由注册、中间件链、SPA 托管 |
| api.go | Dashboard、Services、Users、Network、IPv6 轮换、DNS 同步 |
| middleware.go | `requireRole` 认证、`isInternalRequest`、UserInfo |
| ratelimit.go | Per-IP 限流（20 req/10s → 自动封禁 30min） |
| ipblocker.go | 持久 IP 黑名单中间件 |
| xray_clients.go | Xray 客户端存储（xray-clients.json）、CRUD |
| handlers/users.go | 客户端配置生成（Reality + XHTTP） |
| xray.go | Xray 二进制下载 + SHA256 校验 |
| claude.go | Claude Code 二进制下载 + SHA256 校验 |
| cloudflare.go | Cloudflare DNS API（AAAA 记录 upsert/find） |
| deploy.go | 启动时生成：xray.json、nginx.conf、htpasswd、internal token |

### ui/ — Svelte 5 SPA

Vite 构建，由 admin Go 二进制从 `/app/ui` 托管（多阶段 Dockerfile: node → golang → alpine）。

### services/xray/ — Xray VLESS 代理

- IPv4 via bridge，IPv6 via ipvlan
- Inbounds: `xhttp-in`（8447，经 Cloudflare）+ `reality-in`（443，Reality 直连）
- **XHTTP 模式必须是 `packet-up`**——上行数据拆为独立 POST 逐包发送，适合不稳定网络；不要改回 `stream-up`
- Clients 用 `email` 字段（= 用户名）做 per-user 策略/流量统计

### services/nginx/ — Nginx + fail2ban

托管 admin SPA，反代 `/api/admin/` → admin:8318，`/proxy/query` → xray:8447。fail2ban 通过 `X-Internal-Token` 调用 admin API 封禁真实客户端 IP。

### docker-compose.yml

3 服务: admin, xray, nginx。网络: `ipvlan_docker`（外部，IPv6 L3）+ `internal_docker`（bridge，172.20.0.0/16）。

## Security — 多层防御

1. **Cloudflare Authenticated Origin Pulls (mTLS)**: 只接受 CF Origin Pull 证书。证书: `cert/cloudflare-origin-pull.pem`
2. **Go rate limiter**: 20 req/10s per IP → 429 → 自动封禁 30min
3. **Go IP blocker**: 持久黑名单 → 403
4. **fail2ban in nginx**: 通过 `INTERNAL_TOKEN` 调用 admin API 封禁真实 IP
5. **Cloudflare DDoS/WAF** 在边缘
6. **htpasswd bcrypt** (`$2y$`): 所有密码用 bcrypt 哈希

> "Never use `RemoteAddr`" 的原因就在这里——所有流量经 Docker bridge (172.20.x.x)，基于 RemoteAddr 的判断会全部失败。必须用 `X-Forwarded-User`（nginx 设置）或 `X-Internal-Token`（内部服务）。

## RBAC — 不要破坏这个

三层链：nginx basic auth → Go middleware chain → 前端路由守卫。

### 请求流

```
rateLimitMiddleware → ipBlockMiddleware → requireRole → handler
```

- `requireRole("admin")`: users.json 中 role=admin
- `requireRole("viewer")`: 任何已认证用户
- `X-Internal-Token` bypass: 内部服务（fail2ban）通过共享 `INTERNAL_TOKEN` 调用 admin API，**这是唯一的绕过方式**

### 页面/API 访问矩阵

| 页面 | API 端点 | 角色 |
|------|---------|------|
| Dashboard | `GET /api/admin/dashboard` | viewer+（admin 看统计，viewer 看个人数据） |
| Services | `GET/POST /api/admin/services/*` | admin |
| Clients | `GET/POST /api/admin/users/xray/*` | viewer+（仅自己的客户端） |
| Users | `POST /api/admin/users/admin/*`, `/roles/set` | admin |
| Security | `GET/POST /api/admin/security/*` | admin |
| Settings | `GET/POST /api/admin/network/*` | admin |
| Downloads | `GET /api/admin/claude-code/stored`, `/downloads/*` | viewer+ |
| Downloads 管理 | `GET/POST/DELETE /api/admin/claude-code/*` | admin |
| Xray 二进制 | `GET /api/admin/xray/stored`, `/downloads/*` | viewer+ |
| Xray 管理 | `GET/POST/DELETE /api/admin/xray/*` | admin |

*Downloads 页面对所有用户可见，但 admin-only 的操作（download/delete）只对 admin 展示。

## IPv6 寻址

- 子网: `/64` from `IPV6_SUBNET` env var
- 静态范围: `{prefix}:1::/80` — Docker 分配（nginx、xray 主 IP）
- 轮换范围: `{prefix}:f::/80` — 第 5 个 hextet = `000f`，DNS 指向此处
- `xray.yinghuoren.com` → 轮换 IP（reality 用域名，非裸 IP）
- 客户端配置: reality 出站用 `xray.yinghuoren.com`，xhttp 用 `yinghuoren.com`

## 关键约定

- **配置字段严格校验**: 修改任何配置（Xray、Nginx、Docker 等）时，必须对照官方源码/文档验证字段名大小写、合法值范围和字段语义，不得凭记忆或猜测填写。验证方法：读取官方 GitHub 仓库的 Go struct JSON tag 或官方文档。
- **Never use `RemoteAddr` for auth** — 所有流量经 Docker bridge，看起来都是内网地址。
- **Internal auth**: `INTERNAL_TOKEN`（启动时自动生成，存储在 `.wework/.internal-token`）。
- **Secrets**: gitignored `configs/`、`cert/`、`.wework/`。本地 `.env` 覆盖默认值。
- **Xray 客户端归属**: 非 admin 用户只能管理自己的 Xray 客户端（每人一个）。Admin 可看全部。
- **Git**: linear history on main，one commit per feature。Push → SSH pull → build → up。
- **配置文件写入**：统一使用 `atomicWriteFile`（写 .tmp → rename），防止写到一半崩溃导致配置损坏。禁止直接 `os.WriteFile` 写配置文件。
- **错误响应**：业务错误用 `writeError(w, msg, code)`（msg 返回客户端）；内部错误（文件 I/O、Docker API、第三方 API 等可能泄露内部状态的）必须用 `writeInternalError(w, err)`（只返回固定 "Internal error"，真实错误记日志）。
- **新增 API 端点**：在 `main.go` 的 `mux.HandleFunc(...)` 区域注册，所有认证端点用 `requireRole("viewer")` 或 `requireRole("admin")` 包裹；健康检查用无认证的裸 handler。
- **环境变量**：通过 `getEnv()` 读取（见 `env.go`），不直接用 `os.Getenv`。
