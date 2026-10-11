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

## Cloudflare 优选入口 opt.yinghuoren.com（2026-10-10 实测建）

- 背景：客户端 → CF 边缘（免费版对武汉电信调度差，RTT 240-280ms，晚高峰 TLS 0.5-6s 且常超时）→ mTLS 回源。晚高峰排障结论：服务器与源站全程健康，瓶颈全在「客户端→CF 边缘」段。
- 方案：opt.yinghuoren.com = DNS only 的 A 记录，直接指向实测最优的 CF anycast 边缘 IP。客户端 Xray 配置只改 address 为 opt.yinghuoren.com，serverName/Host/SNI 仍必须是 yinghuoren.com（SNI 用 opt 域名会证书不匹配）。
- 建于 2026-10-10 晚高峰的实测数据（武汉电信，每 IP 3 次）：104.26.1.1 tcp 0.127s / TLS 0.26s 零波动（首选）；162.159.128.1 次之偶波动（备份）。默认解析的 104.21.22.193 / 172.67.206.223 有 2.5-8s 坏样本；v6 各段全面劣于这两个 v4，故暂未配 AAAA——以后要 v6 须先同样手法实测再加。
- 2026-10-11 18:10 复测（高峰前夕）：CF 路由整体优化，出现 0.07s connect 精英梯队（104.26.1.1/5.1/6.1、162.159.128.1/135.1/136.1 全部 3/3、TLS 0.14-0.16s），真实代理下载 1.4-6 MB/s（前夜高峰仅 100-200KB/s）。现有两条 A 记录均在精英梯队且分属 104.26/16 与 162.159/16 两个前缀（路由分集），无需更换。候补：104.26.5.1、162.159.136.1。
- **2026-10-11 21:50 换 IP（自有测速端点定案）**：候选池为 CF 官方 v4 段采样 + 历史精英（62 个）。
  **只能用自己的 `/speedtest/{size}` 端点直连测**：`curl --noproxy '*' --resolve yinghuoren.com:443:<候选IP> https://yinghuoren.com/speedtest/{0|8388608}` —— 它测的是"客户端→CF 候选边缘→CF 回源→nginx→admin"的完整链路，与客户端实际访问路径一致；TTFB(size=0) 与吞吐(size=8MiB) 都要看。
  12 个候选 × 5 TTFB + 4 吞吐的结论：**`104.26.6.1` + `162.159.131.1`**（前者吞吐最差 4.68 / 中位 4.96 MB/s；后者最差 5.12 / 中位 5.45 MB/s 且 TTFB 546/567ms 双指标第一）。原 A 记录均已退化：`162.159.128.1` TTFB 最差 2531ms，`104.26.1.1` 吞吐最差仅 2.77 MB/s。现役：**104.26.6.1 + 162.159.131.1**（TTL 300、DNS only、分属两个 /16 保持路由分集）。候补：162.159.133.1(536ms/4.97)、162.159.136.1、104.26.15.1、104.26.0.1。
  测量陷阱（本次全部踩过，下次直接避开）：
  1. **端点选错会让排名完全反过来**：先用 `speed.cloudflare.com/__down` 测时选出 `162.159.138.1`，改用自有 /speedtest 后它的吞吐掉到 3.91、TTFB 一度 1039ms（两倍于其他候选）。原因是路径不同——CF 公开端点的数据在 CF 网内产生，不经过"回源→nginx→admin"。
  2. **不要用 xray 隧道 + 自有端点**：服务端 freedom 出站再访问 `yinghuoren.com` 会绕第二圈 CF，实测 30s 只下 286KB。优选测量一律用上面的 curl --resolve 直连。
  3. 本机 shell 带 `http_proxy/https_proxy=127.0.0.1:10809`，测速/探测必须 `--noproxy '*'`，否则测到的是"服务器→CF"视角。
  4. 经 CF 访问 `/nginx-health` 返 404 属正常（443 上无该 location，落 `location /`），404 恰证明回源成功；真失败是连接层 `000`。
  5. CF API token 的 IP 白名单**只放服务器出口 IP（用户有意设计，2026-10-11 确认）**：本机直连调用一律被拒（错误码 9109，v4/v6 相同）。正确且唯一用法是本机持有 token + 经本机 xray http 入站代理（`http_proxy=http://127.0.0.1:10809`），出口即服务器。不要把家宽 IP 加白（那是拆掉这道防线），也不要把 token 复制到服务器（凭据红线）。
  6. 该时段抖动大（同一 IP 分钟级在 2.1↔5.7 MB/s 波动），必须同时间窗多采样，且**"吞吐最差"优先于"吞吐中位"**判断。
  4. 该时段速度波动极大（同一 IP 20 分钟内 2.1↔5.7 MB/s），必须**同时间窗对比** + 双协议多次采样，按"中位 + 最差"双标准判断，单次结果不可信。
- CF 边缘 anycast 质量会漂移：优选 IP 变差时重新探测（curl --resolve yinghuoren.com:443:IP https://yinghuoren.com/nginx-health 测 connect/tls/total，超时 4s）并更新 DNS 记录；TTL 300。
- CF API token（~/work/dogod/cf_token_and_cert/cf_account_token）：**IP 白名单只放服务器出口 IP**（用户有意设计）。因此调用方式固定为"本机读 token + 经本机 xray http 入站（127.0.0.1:10809）代理请求"——请求从服务器出口发出才被放行；本机直连会被 9109 拒绝。不要擅自把家宽 IP 加进白名单，也不要把 token 复制到服务器（凭据红线）。

## XHTTP 数据面：分片上限与 413 静默停滞（2026-10-11 实测定位并修复）

**契约：`nginx client_max_body_size` >= 服务端 `scMaxEachPostBytes` >= 客户端上传分片。**

| 位置 | 参数 | 当前值 | 未配置时的回退 |
|------|------|--------|----------------|
| 服务端 `xray.json.tmpl` | `scMaxEachPostBytes` | 4MiB (4194304) | **1MiB（1000000）** |
| 服务端 `xray.json.tmpl` | `scMaxBufferedPosts`（乱序重组队列容量） | 8 | 30 |
| nginx `/proxy/query` | `client_max_body_size` | 16m | — |
| 客户端（面板生成配置与官方默认） | `scMaxEachPostBytes` | 不配置（=1MiB） | 1MiB |

字段名与回退值以 Xray 26.6.27 源码为准：`transport/internet/splithttp/config.go` 的
`GetNormalizedScMaxEachPostBytes` / `GetNormalizedScMaxBufferedPosts`；JSON 侧是
`infra/conf/transport_internet.go` 的 `SplitHTTPConfig`（`Int32Range`，可写数字）。

**故障机制**：上行 POST body 超过服务端上限时，服务端回 **413 且不读请求体**
（`splithttp/hub.go`），客户端 `PostPacket` 见非 200 → `uploadPipeReader.Interrupt()`，
上行静默停摆；应用侧看到的是**响应体停滞**（请求体写满后 0 字节响应直到超时）。
服务端那句 `Too large upload. scMaxEachPostBytes is set to ...` 是 **Info 级**，生产
`loglevel=warning` 下完全不可见；xhttp 自愈原本只统计 400，故服务端零线索
（413 计数告警已于 2026-10-11 加入 `internal/proxy/xhttp_health.go`，**只通知不重启**——
413 是配置不匹配，重启不改变配置、只会让全体代理周期性抖动）。

**线上排查**（413 是唯一可观测信号，来自 nginx access log）：

```bash
# /proxy/query 响应码分布：413 持续出现 = 客户端分片 > 服务端上限
docker logs --since 30m workwork-nginx 2>&1 | grep 'POST /proxy/query' | awk '{print $9}' | sort | uniq -c
```

**本地复现**（容器跑服务端 + 宿主机跑客户端；明文 TCP xhttp 即可，无需证书）：

- 服务端 1MiB 上限 + 客户端 3MiB 分片 → 10MB 上传卡死 15s，目标 0 字节，服务端 2 次 413；
- 服务端 4MiB 上限 + 客户端 3MiB 分片 → 10MB 上传 0.28s，200，目标收全 10485760 B。

**陷阱**：目标服务在宿主机时，Xray 26.6.27 的 freedom 出站**默认黑洞私网地址**
（`proxy/freedom/freedom.go` 的 `defaultBlockPrivateRule`：0.0.0.0/8、10/8、127/8、
172.16/12、192.168/16、fc00::/7 等）。测试配置需临时加
`"settings": {"finalRules": [{"action": "allow"}]}` 才能打到宿主机，否则日志出现
`proxy/freedom: blocked target: ... blackholing connection`，会把"目标不可达"误判成传输故障。
复现材料见 `.workwork/xhttp-post-limit-verify/`（服务端/客户端配置与各组日志，中间产物，勿依赖长期保留）。

