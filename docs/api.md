# Admin API 参考

admin 面板的 HTTP API 契约（唯一事实来源是 `services/admin/main.go` 的路由注册
与各 handler；本文与其冲突时以代码为准并修正本文）。

## 通用约定

- **Base path**：`/api/admin`（经 nginx basic auth + `X-Forwarded-User` 认证；
  浏览器 fetch 用 `credentials: 'same-origin'` 即可）。
- **信封**：所有 JSON 响应为
  `{"success": true, "data": …}` 或 `{"success": false, "error": "人类可读文案", "error_code": "机器可读码"}`。
- **error_code 恒非空**：调用方未给专用码时按 HTTP 状态派生默认码
  （`BAD_REQUEST`/`UNAUTHORIZED`/`FORBIDDEN`/`NOT_FOUND`/`CONFLICT`/`GONE`/
  `RATE_LIMITED`/`INTERNAL`/`BAD_GATEWAY`/`UNAVAILABLE`/`TIMEOUT`/`HTTP_<n>`）；
  专用码（如 `CONFIG_CONFLICT`、`INVALID_DURATION`、`BLOCK_NOT_FOUND`）在
  需要分支的端点标注于下表。
- **错误文案统一为中文**；前端 toast 原样透传 `error`。
- **变更类请求**（POST/PUT/DELETE）校验 `Origin` 与 `Host` 一致（CSRF 防线），
  跨站写请求 403。
- **限流**：20 请求/10s/IP（IPv6 按 /64 聚合），超限 429 `RATE_LIMITED` 并自动
  封禁 30 分钟；公开分享下载 `GET /api/transfer/*` 豁免（由 nginx 单独限速）。
- **请求体上限**：默认 1MB；大上传端点豁免并在 handler 内自限
  （见各端点标注）。
- **审计**：变更类操作成功时服务端记 `[security]` 行（含 actor），不进入响应。

## 角色说明

- **viewer+**：任何已认证用户；**admin**：users.json 中 `role=admin`。
- `X-Internal-Token`（外部自动化）与 `X-Fail2Ban-Token`（仅 block/unblock）
  按 AGENTS「Security」节规则豁免认证。

## 端点总表

### 认证 / 账户 / 用户管理

| 端点 | 角色 | 说明 |
|------|------|------|
| `GET /auth/me` | viewer+ | 当前用户信息与角色 |
| `GET /users` | viewer+ | 全量用户+客户端视图（admin 返回 generatedJSON；viewer 只见自己）。面板用户数天然有限，未分页 |
| `POST /users/password` | viewer+ | 修改自己密码。`{username, old_password, new_password}`；viewer 只能改自己 |
| `POST /users/admin/add` | admin | 创建/更新管理员。`{username, password, role}` |
| `POST /users/admin/delete` | admin | 删除用户（级联删 htpasswd 行、代理凭证、xray 客户端、transfer 上传）。`{username}` |
| `POST /users/roles/set` | admin | 设置角色。`{username, role}` |

错误码要点：`403` 内置管理员保护（不可删/不可改角色）；`400` `LAST_ADMIN`
（最后一个 admin 不可删/不可降级，降级与删除两类端点均返回）。

### Xray 客户端（每人一个，viewer 管理自己的）

| 端点 | 角色 | 说明 |
|------|------|------|
| `POST /users/xray/add` | viewer+ | `{uuid}`。重复 UUID 409；非 admin 且已有客户端 400 |
| `POST /users/xray/delete` | viewer+ | `{uuid}`，只能删自己的 |
| `GET /users/xray/config/{uuid}` | viewer+ | 该客户端的完整客户端配置 JSON（路径中的 UUID 在日志中脱敏） |

### Dashboard / 服务 / 日志

| 端点 | 角色 | 说明 |
|------|------|------|
| `GET /dashboard` | viewer+ | admin 返回 docker 概览+服务列表（含 `health` 字段）；viewer 返回个人数据 |
| `GET /services`、`GET /services/` | admin | 服务列表（name/state/health/image/ports） |
| `POST /services/{name}/{action}` | admin | start/stop/restart（ip 补挂自动进行）。docker daemon 读路径 10s deadline |
| `GET /services/{name}/logs?tail=` | admin | 尾部日志（≤10000 行） |
| `GET /services/{name}/logs/stream?tail=` | admin | SSE 实时日志流 |
| `GET /config` | viewer+ | xray 入站/客户端计数 |

### 网络（IPv6 轮换 / DNS）

| 端点 | 角色 | 说明 |
|------|------|------|
| `GET /network/status` | admin | DNS 记录与容器 IP 对照（整体 20s deadline） |
| `POST /network/rotate-ipv6` | admin | 轮换 xray/nginx 轮换段 IPv6 并更新 DNS（3min 上限） |
| `POST /network/sync-dns` | admin | 按容器当前 IP 重写 DNS |

### 安全（封禁 / 限流）

| 端点 | 角色 | 说明 |
|------|------|------|
| `GET /security/blocked` | admin | 封禁列表。可选 `?limit=&offset=`（1≤limit≤1000） |
| `POST /security/block` | admin | 封禁。`{ip, reason?, duration?}`（秒,0=永久，上限 10 年）。专用码 `INVALID_IP`/`INVALID_DURATION` |
| `POST /security/unblock` | admin | 解封。`{ip}`。未封禁时 `BLOCK_NOT_FOUND` |
| `GET /security/rate-limit` | admin | 限流封禁中的 IP 列表 |
| `POST /security/rate-limit/unblock` | admin | 限流解封。`{ip}` |

### 流量统计

| 端点 | 角色 | 说明 |
|------|------|------|
| `GET /traffic` | admin | 按用户累计代理流量：`{traffic: [{user, up, down}], collected_at}`（xray StatsService，每分钟增量采集，重启累计）。数据缺失（旧 xray/上游故障）时 traffic 为空数组 |

（media/music 已拆分至独立站，其 API 契约见各自仓库 `media_center` / `music_player`；
旧前缀由 Cloudflare 边缘 308 重定向承载，本面板不再提供。）

### 下载分发（Claude Code / Xray 二进制）

| 端点 | 角色 | 说明 |
|------|------|------|
| `GET /claude-code/releases`、`GET /xray/releases` | admin | GitHub 版本列表（30min 缓存） |
| `POST /claude-code/refresh`、`POST /xray/refresh` | admin | 强制刷新缓存 |
| `GET /claude-code/platforms/{version}`、`GET /xray/platforms/{version}` | admin | 版本可用平台 |
| `POST /claude-code/download`、`POST /xray/download` | admin | 服务端下载（NDJSON 进度流；500MB/5min 上限，sha256 入 manifest） |
| `GET /claude-code/stored`、`GET /xray/stored` | viewer+ | 已缓存版本 |
| `GET /claude-code/downloads/{filename}`、`GET /xray/downloads/{filename}` | viewer+ | 下载缓存文件 |
| `DELETE /claude-code/downloads?filename=`、`DELETE /xray/downloads?filename=` | admin | 删除缓存文件（query 传参，不带 body）；另有带尾斜杠的别名路由同 handler |

### 文件中转（transfer）

| 端点 | 角色 | 说明 |
|------|------|------|
| `POST /transfer/upload` | viewer+ | multipart（≤2GB/文件；全局 20GB、每用户 5GB 配额；并发闸门 2 位） |
| `GET /transfer/files` | viewer+ | 自己的列表（admin 全量）。可选 `?limit=&offset=`；响应含 `total_size`/`max_total` |
| `DELETE /transfer/files?code=` | viewer+ | 删除（viewer 只能删自己的） |

### 公开端点（无 basic auth）

| 端点 | 说明 |
|------|------|
| `GET /api/transfer/{code}` | 分享码下载（nginx 1r/s + 3 并发限速；Go 限流豁免） |
| `GET /api/admin/health` | 健康检查（无认证）：`{status, version}`，未就绪 503 |

## 前端对接要点

- `src/lib/api.js` 是唯一封装层：信封拆包、30s 默认超时（可覆盖）、
  `AbortSignal.any` 合并外部 signal、`ApiError{message, code, details}`。
- 错误 toast 原样透传 `error` 文案；需要分支时判断 `e.code`。
- 列表分页参数当前前端未使用（默认全量）；接入时传 `limit`/`offset` 即可。
