---
name: workwork-security
description: workwork 安全与网络：多层防御（CF Origin Pulls mTLS / CSRF / 限流 / IP 封禁 / fail2ban / docker API 白名单）、RBAC 三层链与请求流、X-Internal-Token 与 X-Fail2Ban-Token 语义、XHTTP 数据面契约、Cloudflare 优选入口。触发：改认证/鉴权/限流/封禁代码；新增 API 端点或页面；排查 401/403 或限流误伤；动 nginx/xray 网络配置；改 docker-proxy 白名单；优选 IP 或排查代理链路。
---

# workwork 安全与网络

## 多层防御（红线）

按流量经过顺序排列。改动某一层前，先读 `references/defense-layers.md` 对应小节。

1. **CF Authenticated Origin Pulls (mTLS)**：`ssl_verify_client on` 只接受 CF Origin Pull 证书；校验用的 CA 在镜像内 `/etc/nginx/cloudflare-origin-pull.pem`（构建时从 CF 官方 `authenticated_origin_pull_ca.pem` 下载，见 `services/nginx/Dockerfile`），不是仓库 `cert/` 下的文件。
2. **CSRF 中间件**（Go，变更类请求）：浏览器发起的写请求 `Origin` 必须与 `Host` 一致（`auth.CSRFMiddleware`）。它必须位于限流**之前**——先限流会让伪造请求耗尽受害者 IP 的配额。
3. **Go rate limiter**：20 req/10s → 429 → 自动封禁 30min；IPv6 按 **/64 网段**聚合计数（换后缀不重置预算），封禁仍按具体 IP。**解封 API 必须两个维度都清**（整点计数 + /64 前缀预算），只清前者同前缀仍被拦截。`GET /api/transfer/*` 公开下载豁免（nginx 已有 1r/s + 3 并发约束）。
4. **Go IP blocker**：持久黑名单 → 403。
5. **nginx 层**：`limit_req admin_api`（10r/s burst 50）+ `blocked-ips.conf` return 403，对**携带合法 internal token 的请求豁免**（admin 把 token 真实值渲染进 nginx map；nginx 值匹配，伪造头不匹配，照常限流/封禁。应用层仍需恒时比较作为第二道）。`transfer_dl/transfer_conn` 刻意不豁免。
6. **fail2ban（nginx 内）**：经 scoped `X-Fail2Ban-Token` 调 admin block/unblock API 封真实 IP。计数语义：filter 只计**带错误凭证**的 401，浏览器冷启动质询的无凭证 401 不计入，maxretry=5 即真实错密码 5 次。误封风险与验证手法见 references/defense-layers.md。
7. **Cloudflare DDoS/WAF** 在边缘；**htpasswd bcrypt**（`$2y$`）哈希全部密码。
8. **docker API 白名单代理**（`services/docker_proxy`）：`(method, path)` 逐条锚定，匹配前先剥 query，**禁止二次 URL 解码**（`%252F` 会让「检查的串」与「上游执行的串」不一致）；容器级路径靠 compose 项目归属校验，无容器句柄的读端点必须自带项目 label 过滤，一律 fail-closed。**加新 docker 调用：容器级端点靠归属校验，列表/事件端点必须自带 `platform.ComposeProjectFilters(...)`，否则运行期 403**；`ALLOWED_COMPOSE_PROJECTS` 由 compose 从 `COMPOSE_PROJECT_NAME` 接线，两者不一致会让本栈容器被整体拒绝、面板操作静默失效。细节与本地 e2e 测试见 references/defense-layers.md。
9. **工具下载完整性**：admin 的 claude/xray 安装包下载比对 GitHub release asset 的 `digest`（`sha256:<hex>`），校验在 `rename`+manifest 落盘**之前**——下载目录会被 `/api/admin/*/downloads` 分发给所有代理客户端，未校验字节落库即出库。上游缺摘要或非 sha256 时跳过校验而不拒绝下载。细节见 references/defense-layers.md。

**Never use `RemoteAddr`**：所有流量经 Docker bridge 内网（网段由 Docker 默认地址池动态分配），基于 RemoteAddr 的判断会全部失败；必须用 `X-Forwarded-User`（nginx 设置）或 `X-Internal-Token`（内部服务）。

## RBAC — 不要破坏这个

三层链：nginx basic auth → Go middleware chain → 前端路由守卫。

```
auth.CSRFMiddleware → mw.IPBlockMiddleware → mw.RateLimitMiddleware → mw.RequireRole → handler
```

- `mw.RequireRole("admin")`：users.json 中 role=admin；`mw.RequireRole("viewer")`：任何已认证用户。
- `X-Internal-Token` / `Authorization: Bearer <INTERNAL_TOKEN>`：全权限共享 token（nginx 透传，外部自动化经公网可用），持合法 token 四层全豁免（nginx 限速+封禁、admin 限流+封禁）；配 `INTERNAL_TOKEN_ALLOWED_PREFIXES` 后只豁免白名单路径前缀，其余路径退回正常认证；伪造 token 在 admin 校验失败后照常受罚。
- `X-Fail2Ban-Token`：scoped bypass，只允许 fail2ban 调 `POST /api/admin/security/block|unblock`；nginx 始终清空，仅限内部。
- 页面/API 访问矩阵见 [`docs/architecture.md`](../../../docs/architecture.md#rbac-访问矩阵)；新增端点按最小角色选 `viewer`/`admin`。

### 前端 adminOnly 的两种语义（别把「URL 能进」当漏洞）

`routes.js` 的 `routes[].adminOnly` 是 SPA 路由守卫（App.svelte 显示 forbidden 页）；`navSections[].items[].adminOnly` 只控制侧边栏是否显示，**两者独立**。`/users` 就是「侧边栏对 viewer 隐藏、路由对 viewer 开放」的既有设计：页面管理内容全包在 `{#if isAdmin}`，viewer 进去只有「修改密码」卡片（`POST /users/password` 为 viewer+，见 docs/api.md）；若给 `routes` 那条也加 adminOnly，viewer 连按 URL 都进不去，等于删掉唯一的自助改密入口。

判定顺序：① 读目标页面有没有 `!isAdmin` 分支或调用 viewer+ 接口；② 看侧边栏是否已隐藏该条目——只隐藏导航属 UX 取舍，不是权限缺口；③ 组件内按钮即便对 viewer 显示，真正拦截在服务端 `requireRole`，前端隐藏只是体验优化，改它前先确认没删掉合法功能。

## XHTTP 数据面：分片上限与 413 静默停滞

**契约：`nginx client_max_body_size` >= 服务端 `scMaxEachPostBytes` >= 客户端上传分片。**

| 位置 | 参数 | 当前值 | 未配置时的回退 |
|------|------|--------|----------------|
| 服务端 `xray.json.tmpl` | `scMaxEachPostBytes` | 4MiB (4194304) | **1MiB（1000000）** |
| 服务端 `xray.json.tmpl` | `scMaxBufferedPosts`（乱序重组队列容量） | 8 | 30 |
| nginx `/proxy/query` | `client_max_body_size` | 16m | — |
| 客户端（面板生成配置与官方默认） | `scMaxEachPostBytes` | 不配置（=1MiB） | 1MiB |

**故障机制**：上行 POST body 超服务端上限时，服务端回 **413 且不读请求体**（`splithttp/hub.go`），客户端 `PostPacket` 见非 200 即 `uploadPipeReader.Interrupt()`，上行静默停摆；应用侧只看到响应体停滞。服务端那句 `Too large upload...` 是 **Info 级**，生产 `loglevel=warning` 下不可见。413 计数告警在 `internal/proxy/xhttp_health.go`（**只通知不重启**——413 是配置不匹配，重启不改变配置、只会让全体代理周期性抖动）。

**线上排查**（413 是唯一可观测信号，来自 nginx access log）：

```bash
# /proxy/query 响应码分布：413 持续出现 = 客户端分片 > 服务端上限
docker compose logs --since 30m nginx 2>&1 | grep 'POST /proxy/query' | awk '{print $9}' | sort | uniq -c
```

**本地复现**：容器跑服务端 + 宿主机跑客户端，明文 TCP xhttp 即可（无需证书）——1MiB 上限 + 3MiB 分片时 10MB 上传卡死 15s、目标 0 字节、服务端 2 次 413；4MiB 上限 + 3MiB 分片时 0.28s、200、目标收全。完整步骤、字段源码出处与 freedom 私网黑洞陷阱见 references/xhttp-dataplane.md。

## Cloudflare 优选入口 opt.yinghuoren.com

- **现役**：`opt.yinghuoren.com` = DNS only 的 A 记录，指向 **162.159.154.177 + 162.159.158.122**（TTL 300，分属两个 /16 保持路由分集）。它是客户端到 CF 边缘的最优 anycast 入口，源站回源仍走域名的 mTLS。
- 客户端 Xray 配置只改 `address` 为 `opt.yinghuoren.com`，`serverName`/`Host`/SNI 仍必须是 `yinghuoren.com`（SNI 用 opt 域名会证书不匹配）。面板生成的配置已内建这个拆分：服务端 `.env` 设 `XHTTP_ADDRESS=opt.yinghuoren.com`（`XHTTP_DOMAIN` 保持 yinghuoren.com 作 SNI），改后重启 admin 容器生效；手工改本机 `config.jsonc` 只作临时验证手段。
- 换 IP 判定**只能用自有 `/speedtest/{size}` 端点直连测**，且必须 `--noproxy '*'`；同窗口用 cfst 单连接 + 4 连接聚合双法交叉复核（单连接排名跨窗口必翻车）：
  ```bash
  ./cfst -url "https://yinghuoren.com/speedtest/33554432" -dn 10 -t 4 -p 15 -o result.csv
  ```
  cfst 2.5 分钟完成全段粗筛 + top10 下载测速；完整方法论、扫描分阶段纪律与陷阱见 references/edge-opt.md。
- CF API token 的 IP 白名单**只放服务器出口 IP**（有意设计）：本机直连调用被拒（错误码 9109），正确用法是本机持 token + 经本机 xray http 入站代理（`http_proxy=http://127.0.0.1:10809`），出口即服务器。**不要把家宽 IP 加白，也不要把 token 复制到服务器**（凭据红线）。
- h3/QUIC 晚高峰不可用：`XHTTP_ALPN=h3` 客户端晚高峰 QUIC 出站整段挂死（curl 000、15s 超时），同刻 h2/TCP 正常，属运营商对 UDP 443 的 QoS。面板默认 `XHTTP_ALPN=h2`；晚高峰代理挂掉先查这里（服务器 `.env` 一行 + 重启 admin）。`prioritizeIPv6` 必须为 `false`（`true` 会拖死无 AAAA 的目标，如 github.com），排障手法见 references/edge-opt.md。

## IPv6 寻址

寻址规则见 [`docs/architecture.md`](../../../docs/architecture.md#ipv6-寻址)。
