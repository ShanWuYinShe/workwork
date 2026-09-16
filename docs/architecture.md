# 架构说明

本文件是 workwork 的设计叙事（服务分工、网络拓扑、IPv6 寻址、RBAC 矩阵），给人看、给 AI 按需读。**唯一事实来源是代码与 `docker-compose.yml`**；与实现冲突时以代码为准，并把本文档改对（不要反过来按文档改代码）。

## 服务分工

### services/admin/ — Go 管理面板（端口 8318，内部）

HTTP 服务，涵盖 Dashboard / 用户管理 / 网络配置 / Xray 客户端 CRUD / 安全封禁 / Cloudflare DNS / 部署配置生成。单容器多阶段构建（bun → golang → alpine），通过 `docker.sock` 操作其他容器。

### services/admin/ui/ — Svelte 5 SPA

Vite 构建，由 admin Go 二进制从 `/app/ui` 托管（多阶段 Dockerfile: oven/bun → golang → alpine）。源码随 admin 仓库目录（services/admin/ui/），非独立部署单元。

### services/admin/configs/ — 配置模板

`nginx.conf.tmpl` / `xray.json.tmpl` 的唯一读者是 admin（启动时渲染到 `.wework/generated/`），随 admin 镜像构建期 COPY 分发——模板版本与二进制版本原子对应，改模板 = rebuild admin。

### services/xray/ — Xray VLESS 代理

- 单容器双网络：IPv4 出站 via 专用 `xray_egress` bridge NAT；IPv6 via ipvlan L3。**IPv4 入站 443 由 `xray-forward` 转发容器发布**（纯 bridge + socat，`0.0.0.0:443:443` → `xray:443`），xray 本身不声明 ports。原因：xray 容器连接 ipvlan 网络后，Docker(29.x) 会把 ipvlan 选为默认网关网络(gwep4)，该容器所有端口映射整体失效（ipvlan 驱动不支持端口映射，与网络连接顺序无关，已实测）。不要让 xray 加入 `internal_docker`
- Inbounds: `xhttp-in`（Unix socket `/run/xray/xrxh.socket`，nginx `/proxy/query` 转发，Cloudflare 前置；无独立 TCP 端口）+ `reality-in`（443，Reality 直连）
- XHTTP 模式必须是 `packet-up`——上行数据拆为独立 POST 逐包发送，适合不稳定网络；不要改成 `stream-up`
- 客户端双出站 reality + xhttp，observatory 探测 + leastLoad 均衡自动选路；客户端内置并行 DNS 段加速代理域名解析
- Clients 用 `email` 字段（= 用户名）做 per-user 策略/流量统计

### services/nginx/ — Nginx + fail2ban

托管 admin SPA，反代 `/api/admin/` → admin:8318，`/proxy/query` → xray Unix socket。fail2ban 通过 scoped `X-Fail2Ban-Token` 调用 admin block/unblock API 封禁真实客户端 IP。

### services/media_center/media_server/ — Media Center 目录服务（git submodule）

目录服务：Go HTTP，从 catalog(SQLite) 提供 provider 列表、分类列表、视频元数据。播放按 provider 分发：bilibili 由 App 端 WBI 签名本地解析；youtube 由 server 端两个受控实时端点处理（`/api/providers/youtube/search` 经 `vod_extractor`（yt-dlp ytsearch）实时搜索、`/api/providers/youtube/resolve` 代理 `vod_extractor` 解析播放直链，均要求 X-App-Token + 独立限流，见 `media_server/youtube.go`）。不做 asset 代理。源码以 git submodule 形式引入自 `media_center` 仓库（`git submodule update --init --recursive` 拉取）。线上由 admin 生成运行时分类配置，nginx 默认通过 `/api/media` 转发到 `media_server:8787`。

## docker-compose

8 服务: admin, xray, xray-forward, nginx, media_server, vod_extractor, netease_api, music_server。网络: `ipvlan_docker`（外部，IPv6 L3）+ `internal_docker`（admin/media/music/netease 内网 bridge，网段由 Docker 默认地址池动态分配）+ `xray_egress`（xray 专用 IPv4 egress bridge）。`xray` 持有公网 IPv6（ipvlan L3 直连），IPv4 直连 443 由 `xray-forward`（bridge-only socat 转发容器）发布，转发目标 `xray:443`（xray_egress 网络内）。不要为 IPv4 再加其他转发方式（宿主 iptables DNAT 等）——Docker 管理的端口映射已足够。
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
