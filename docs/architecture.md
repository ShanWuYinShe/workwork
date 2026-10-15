# 架构说明

本文件是 workwork 的设计叙事（服务分工、网络拓扑、IPv6 寻址、RBAC 矩阵），给人看、给 AI 按需读。**唯一事实来源是代码与 `docker-compose.yml`**；与实现冲突时以代码为准，并把本文档改对（不要反过来按文档改代码）。

## 服务分工

### services/admin/ — Go 管理面板（端口 8318，内部）

HTTP 服务，涵盖 Dashboard / 用户管理 / 网络配置 / Xray 客户端 CRUD / 安全封禁 / Cloudflare DNS / 部署配置生成。单容器多阶段构建（bun → golang → alpine），通过自建白名单代理（`docker-proxy` 服务，compose 独立网络 `docker_proxy`，仅 admin 可达）操作其他容器：method+path 级放行 containers 读写/exec/events/info/version/ping，create/delete/images/networks/volumes/swarm 一律 403（容器 create + bind-mount 是宿主逃逸面，必须拒绝）。引用具体容器的路径额外校验 compose 项目归属；没有容器句柄的列表/事件端点要求调用方自带项目 label 过滤，缺失即 403（info/version/ping 不含容器元数据，直接放行）。

### services/admin/ui/ — Svelte 5 SPA

Vite 构建，由 admin Go 二进制从 `/app/ui` 托管（多阶段 Dockerfile: oven/bun → golang → alpine）。源码随 admin 仓库目录（services/admin/ui/），非独立部署单元。

### services/admin/configs/ — 配置模板

`nginx.conf.tmpl` / `xray.json.tmpl` 的唯一读者是 admin（启动时渲染到 `.wework/generated/`），随 admin 镜像构建期 COPY 分发——模板版本与二进制版本原子对应，改模板 = rebuild admin。

### services/xray/ — Xray VLESS 代理

- 单容器双网络：IPv4 出站 via 专用 `xray_egress` bridge NAT；IPv6 via ipvlan L3。出站拨号 IPv6 优先、IPv4 兜底（freedom 出站 `happyEyeballs.prioritizeIPv6: true` + DNS `UseIP`：双栈目标走 v6，仅 v4 目标落回 NAT）。**无公网入站端口**（XHTTP 经 nginx Unix socket 接入；曾有的 Reality 443 入站与 `xray-forward` 转发容器已移除）。注意：xray 容器连接 ipvlan 网络后，Docker(29.x) 会把 ipvlan 选为默认网关网络(gwep4)，该容器所有端口映射整体失效（ipvlan 驱动不支持端口映射，与网络连接顺序无关，已实测）——若未来需要入站端口，不能直接声明 ports。不要让 xray 加入 `internal_docker`
- Inbounds: `xhttp-in`（Unix socket `/run/xray/xrxh.socket`，nginx `/proxy/query` 转发，Cloudflare 前置；无独立 TCP 端口）
- XHTTP 模式必须是 `packet-up`——上行数据拆为独立 POST 逐包发送，适合不稳定网络；不要改成 `stream-up`
- XHTTP 分片契约：服务端 `scMaxEachPostBytes` 是单个上行 POST body 上限（`xray.json.tmpl` 显式 4MiB；同时 `scMaxBufferedPosts: 8` 把乱序重组队列最坏内存压在 ~32MiB，与容器 128MiB 上限匹配），nginx `/proxy/query` 的 `client_max_body_size` 必须 ≥ 它（当前 16m）。Xray 客户端该字段默认 1MiB：**服务端上限小于客户端分片时，服务端回 413 且不读 body，客户端上行静默停摆、表现为响应体停滞**（xray 该日志为 Info 级，生产 loglevel=warning 不可见，故由 admin 的 xhttp 自愈按 413 计数外发告警）。因此服务端只放宽上限，客户端保持默认小分片。
- 服务端内置 DNS 取两个独立厂商的加密解析（Cloudflare `1.1.1.1` + Google `8.8.8.8`；`https+local` 即 DOHL——DoH 加密且不过路由组件）+ 同名明文 UDP 兜底 + `localhost`（系统解析器），`enableParallelQuery` 并行取最快：单一 DoH 厂商故障时 freedom 出站的域名解析仍有备份，不会整体失败（2026-10-12 服务器侧实测候选解析器均可达、证书含对应 IP SAN）
- 客户端单出站 xhttp（经 Cloudflare + nginx TLS 终结）；客户端内置并行 DNS 段加速代理域名解析
- Clients 的 `email` 字段（= 用户名）用于标识客户端归属（面板里"谁拥有哪个客户端"），同时是 per-user 流量统计的计数器键：模板开启 `statsUserUplink/Downlink` + 内置 StatsService API（容器内 127.0.0.1:10085 dokodemo inbound，routing 首条规则回环），admin 每分钟 docker exec `xray api statsquery` 增量聚合（`internal/proxy/traffic.go`），状态落盘 `.wework/traffic-state.json` 兜住双方重启；展示在 Clients 页与 `GET /api/admin/traffic`

### services/nginx/ — Nginx + fail2ban

托管 admin SPA，反代 `/api/admin/` → admin:8318，`/proxy/query` → xray Unix socket。fail2ban 通过 scoped `X-Fail2Ban-Token` 调用 admin block/unblock API 封禁真实客户端 IP。

### media/music — 已拆分至独立站

媒体中心（media_server + vod_extractor）与在线音乐（music_server + netease_api）已拆分为独立网站 `media.chaoset.com` / `music.chaoset.com`，源码、CI、部署随各自仓库（media_center / music_player，含独立 deploy/ 编排），与本项目零互通。旧前缀 `/api/media`、`/api/music` 的存量 App 连续性由 CF 边缘动态重定向（308 → 各新站 `/api/*`）承载。

## docker-compose

4 服务: admin, docker-proxy, xray, nginx。网络: `ipvlan_docker`（外部，IPv6 L3）+ `internal_docker`（admin 内网 bridge，网段由 Docker 默认地址池动态分配）+ `xray_egress`（xray 专用 IPv4 egress bridge）+ `docker_proxy`（内部，仅 admin 与 docker-proxy）。`xray` 持有公网 IPv6（ipvlan L3 直连），仅作代理出站；公网入站统一经 nginx（IPv6），xray 无入站端口（曾用于发布 Reality 443 的 `xray-forward` 转发容器已随 Reality 移除）。
vod_extractor 仅内网（媒体中心 YouTube 搜索与播放解析，无 Data API 配额）；部署硬前提：宿主机 `.wework/vod_extractor/youtube-cookies.txt`（YouTube cookies，Netscape 格式），缺失时 YouTube 搜索/解析全被 bot 检测拦截。

## RBAC 访问矩阵

| 页面 | API 端点 | 角色 |
|------|---------|------|
| Dashboard | `GET /api/admin/dashboard` | viewer+（admin 看统计，viewer 看个人数据） |
| Services（含 Logs） | `GET/POST /api/admin/services/*` | admin |
| Clients | `GET/POST /api/admin/users/xray/*` | viewer+（仅自己的客户端） |
| Users 管理 | `POST /api/admin/users/admin/*`, `/roles/set` | admin |
| 账户 | `GET /api/admin/auth/me`, `GET /api/admin/users`, `POST /api/admin/users/password` | viewer+ |
| Security | `GET/POST /api/admin/security/*` | admin |
| Settings | `GET/POST /api/admin/network/*` | admin |
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
- 客户端配置: xhttp 出站地址取 `XHTTP_ADDRESS`（未配置则用 `XHTTP_DOMAIN`）——生产指向 Cloudflare 优选入口 `opt.yinghuoren.com`（DNS-only A 记录，指向实测最优的 CF anycast 边缘 IP）；TLS SNI/证书名恒为 `XHTTP_DOMAIN`（`yinghuoren.com`），ALPN 由 `XHTTP_ALPN` 决定（默认 `h2` 走 TCP，`h3` 走 QUIC/UDP 且生成器剔除 TCP 专属 sockopt）。优选入口域名没有边缘证书、不能当 SNI 用（Reality 时代的 `xray.yinghuoren.com` 直连域名已随 Reality 移除，Cloudflare 上的残留 AAAA 记录需手动删除）
- 不需要宿主机 sysctl 配置：ipvlan L3 模式下容器直接持有公网 IPv6，数据包在内核驱动层直接投递到容器命名空间。无需启用 `net.ipv6.conf.all.forwarding`、`proxy_ndp` 等宿主机网络设置。禁止修改宿主机 sysctl/iptables/网络配置。
