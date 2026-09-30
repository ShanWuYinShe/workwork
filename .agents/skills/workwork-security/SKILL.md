---
name: workwork-security
description: workwork 安全与网络：多层防御（CF Origin Pulls mTLS / CSRF / 限流 / IP 封禁 / fail2ban）、RBAC 三层链与请求流/访问矩阵、X-Internal-Token 与 X-Fail2Ban-Token、IPv6 寻址。触发：改认证/鉴权/限流/封禁代码；新增 API 端点或页面；排查 401/403 或限流误伤；动 nginx/xray 网络配置。
---

# workwork 安全与网络

## Security — 多层防御

1. Cloudflare Authenticated Origin Pulls (mTLS): 只接受 CF Origin Pull 证书。证书: `cert/cloudflare-origin-pull.pem`
2. CSRF 中间件（Go，变更类请求）：浏览器发起的写请求 Origin 必须与 Host 一致（`auth.CSRFMiddleware`，位于限流**之前**——先限流会让伪造请求耗尽受害者 IP 配额）
3. Go rate limiter: 20 req/10s → 429 → 自动封禁 30min；IPv6 按 /64 网段聚合计数（换后缀不重置预算），封禁仍按具体 IP；`GET /api/transfer/*` 公开下载豁免（nginx 已有 1r/s + 3 并发约束，多连接下载器勿用 Go 层限流惩罚）
4. Go IP blocker: 持久黑名单 → 403
5. fail2ban in nginx: 通过 scoped `X-Fail2Ban-Token` 调用 admin block/unblock API 封禁真实 IP。计数语义（2026-09-30 起）：filter 只计**带错误凭证**的 401（failregex 按 `$remote_user` 非空区分，`^<HOST> - (?!- )\S+ \[`）——浏览器冷启动质询的无凭证 401 是正常登录流程不计入，maxretry=5 即真实错密码 5 次；登出凭证覆盖请求每次仍计 1 条，故不能取 3。此前全量计数 + maxretry=10 曾两次误封合法用户（5min 内双冷启动 ≈12>10）。验证手法：构造 auth_log 样本行，在 nginx 镜像内 `fail2ban-regex` 断言命中/不命中集合
6. Cloudflare DDoS/WAF 在边缘
7. htpasswd bcrypt (`$2y$`): 所有密码用 bcrypt 哈希

"Never use `RemoteAddr`" 的原因就在这里——所有流量经 Docker bridge 内网地址（网段由 Docker 默认地址池动态分配），基于 RemoteAddr 的判断会全部失败。必须用 `X-Forwarded-User`（nginx 设置）或 `X-Internal-Token`（内部服务）。

## RBAC — 不要破坏这个

三层链：nginx basic auth → Go middleware chain → 前端路由守卫。

### 请求流

```
csrfMiddleware → rateLimitMiddleware → ipBlockMiddleware → requireRole → handler
```

- `requireRole("admin")`: users.json 中 role=admin
- `requireRole("viewer")`: 任何已认证用户
- `X-Internal-Token` bypass: 仅保留给可信内部自动化的全权限共享 token；nginx/fail2ban 不挂载此 token
- `X-Fail2Ban-Token` scoped bypass: 只允许 fail2ban 调用 `POST /api/admin/security/block|unblock`

### 页面/API 访问矩阵

完整矩阵见 [`docs/architecture.md`](../../../docs/architecture.md#rbac-访问矩阵)。新增端点按最小角色选 `viewer`/`admin` 包裹。

## IPv6 寻址

寻址规则见 [`docs/architecture.md`](../../../docs/architecture.md#ipv6-寻址)。
