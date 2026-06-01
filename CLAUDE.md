# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

WorkWork is a Docker-based proxy infrastructure featuring:
- **Xray** VLESS Reality + XHTTP proxy (dual-stack IPv6/ipvlan + IPv4/bridge)
- **Nginx** reverse proxy (Cloudflare Full Proxy → nginx → admin/xray)
- **Admin panel** (Go backend + Svelte SPA) with user management, IPv6 rotation, cliproxy orchestration, model pool, binary downloads
- **Cliproxy** per-user API proxy containers for AI provider access

## Hard constraint — container isolation

All service functionality MUST be isolated within Docker containers. Do NOT modify host environment settings (iptables, sysctl, network config) except for creating Docker networks. IPv4 routing goes through Docker bridge NAT; IPv6 routing through ipvlan L3. The admin service manipulates other containers via docker.sock and `containerExec()`.

## Commands

```bash
# Go admin service — build & compile check
go build -C services/admin -o /dev/null .

# Svelte frontend — build
npm --prefix ui run build

# Full deploy (after git push)
ssh workwork-server "cd service && git pull origin main && docker compose build --no-cache admin nginx && docker compose up -d"
```

## RBAC — Do NOT break this

All access control works through a **three-layer chain**:

### Layer 1: nginx basic auth
- `/admin/` and `/api/admin/` require HTTP basic auth via `.htpasswd`
- nginx sets `X-Forwarded-User` header from auth username
- nginx sets `X-Real-IP` from `CF-Connecting-IP` (Cloudflare Full Proxy)

### Layer 2: Go middleware chain
Request flows: `rateLimitMiddleware → ipBlockMiddleware → requireRole → handler`

- **`requireRole("admin")`**: Only users with `role=admin` in `/app/data/users.json`
- **`requireRole("viewer")`**: Any authenticated user (admin or viewer)
- **`X-Internal-Token` bypass**: Internal services (fail2ban) use shared `INTERNAL_TOKEN` env var to call admin API. This is the ONLY bypass. Never use `RemoteAddr` checks — all requests come through Docker bridge (172.20.x.x).

### Layer 3: Frontend guards
- Nav items with `adminOnly: true` are hidden from non-admin
- Route guards check `isAdmin` before rendering admin components
- `/api/admin/` endpoints MUST match their UI route's adminOnly status

### Page/API access matrix

| Page | Nav | Route | API endpoints | Role |
|------|-----|-------|--------------|------|
| Dashboard | all | all | `GET /api/admin/dashboard` | viewer+ (admin gets stats, viewer gets personal data) |
| Services | admin | admin | `GET /api/admin/services`, `POST /api/admin/services/{name}/{action}` | admin |
| Clients | all | all | `GET /api/admin/users`, `POST /api/admin/users/xray/add`, `POST /api/admin/users/xray/delete` | viewer+ (own clients only) |
| Users | admin | admin | `POST /api/admin/users/admin/add`, `POST /api/admin/users/admin/delete`, `POST /api/admin/users/roles/set` | admin |
| Security | admin | admin | `GET /api/admin/security/blocked`, `POST /api/admin/security/block`, `POST /api/admin/security/unblock` | admin |
| Cliproxy | admin | admin | `/api/admin/cliproxy/*` | admin |
| Settings | admin | admin | `GET /api/admin/network/status`, `POST /api/admin/network/rotate-ipv6`, `POST /api/admin/network/sync-dns` | admin |
| Model Pool | admin | admin | `GET/POST /api/admin/model-pool`, `POST /api/admin/model-pool/users` | admin |
| Downloads | all | all | `GET /api/admin/claude-code/stored`, `GET /api/admin/claude-code/downloads/{filename}` | viewer+ |
| Downloads mgmt | admin | admin | `GET /api/admin/claude-code/releases`, `POST /api/admin/claude-code/download`, `DELETE /api/admin/claude-code/downloads/` | admin |
| Xray binary | all | all | `GET /api/admin/xray/stored`, `GET /api/admin/xray/downloads/{filename}` | viewer+ |
| Xray binary mgmt | admin | admin | `GET /api/admin/xray/releases`, `POST /api/admin/xray/download`, `DELETE /api/admin/xray/downloads/` | admin |

*Downloads page is visible to all but admin-only controls (download/delete) are hidden for viewers.

## Service Architecture

### `services/admin/` — Go admin panel (port 8318, internal)

Single `main` package with `handlers/` sub-package.

| File | Purpose |
|------|---------|
| `main.go` | HTTP route registration, middleware chain, SPA serving |
| `api.go` | Dashboard, Services, Users, Network status, IPv6 rotation, DNS sync |
| `middleware.go` | `requireRole` auth, `isInternalRequest`, UserInfo from `users.json` |
| `ratelimit.go` | Per-IP rate limiter (20 req/10s → auto-ban 30min) |
| `ipblocker.go` | Persistent IP blocklist middleware |
| `xray_clients.go` | Xray client storage (xray-clients.json), CRUD operations |
| `handlers/users.go` | Client config generation (Reality + XHTTP, ordered JSON) |
| `xray.go` | Xray binary download from GitHub with SHA256 verification |
| `claude.go` | Claude Code binary download from official CDN with SHA256 verification |
| `cliproxy.go` | Per-user cliproxy containers, config get/set |
| `modelpool.go` | Public AI model pool: providers config + per-user pool access grants |
| `cloudflare.go` | Cloudflare DNS API (AAAA record upsert/find) |
| `deploy.go` | Startup config generation: xray.json, nginx.conf, cliproxy.yaml, internal token |
| `env.go` | Env var helpers |

### `ui/` — Svelte 5 SPA

Built with Vite, served by admin Go binary from `/app/ui` (built into Docker image via multi-stage Dockerfile: node → golang → alpine).

Routes map to API endpoints:
- `Dashboard.svelte` — system stats (admin) or personal info + config (viewer)
- `Services.svelte` — Docker container management
- `Clients.svelte` — Xray UUID management, client config download
- `Users.svelte` — user CRUD, role management
- `Security.svelte` — IP blocklist + rate limit status
- `Cliproxy.svelte` — cliproxy container management
- `ModelPool.svelte` — model pool config
- `Settings.svelte` — network status, IPv6 rotation, DNS sync
- `Downloads.svelte` — Claude Code + Xray binary downloads (tabbed)

### `services/xray/` — Xray VLESS proxy

- `config.json.tmpl` / `entrypoint.sh` — IPv4 via bridge, IPv6 via ipvlan
- Inbounds: `xhttp-in` (8447, via Cloudflare) + `reality-in` (443, Reality direct)
- XHTTP mode: `packet-up` — 上行数据拆为独立 POST 逐包发送，适合不稳定网络；不要改回 `stream-up`
- Clients have `email` field (=owner username) for per-user policy/traffic stats
- `migrateXrayClients()` ensures all clients have email on startup

### `services/nginx/` — Nginx + fail2ban

- Serves admin SPA, proxies `/api/admin/` to admin:8318, `/proxy/query` to xray:8447
- fail2ban watches auth log for 401s, calls admin API with `X-Internal-Token` to block real client IPs

### IPv6 addressing

- Subnet: `/64` from `IPV6_SUBNET` env var
- Static range: `{prefix}:1::/80` — Docker-assigned IPs (nginx, xray primary)
- Rotating range: `{prefix}:f::/80` — 5th hextet = `000f`, DNS points here
- `xray.yinghuoren.com` → rotating IP (reality uses domain, not raw IP)
- Client config: reality outbound uses `xray.yinghuoren.com`, xhttp uses `yinghuoren.com`

### Cliproxy config format

```yaml
api-keys:                    # User-facing keys to ACCESS cliproxy
  - "key-value"
openai-compatibility:        # OpenAI API format providers
  - name: "provider-name"
    base-url: "https://..."
    api-key-entries:
      - api-key: "sk-xxx"
    models:
      - name: "*"
        alias: "prefix/*"
claude-api-key:              # Anthropic API format providers
  - api-key: "sk-ant-xxx"
    base-url: "https://..."
    models:
      - name: "*"
        alias: "model-alias"
```

### `docker-compose.yml`

4 services: nginx, xray, cliproxy-public, admin. Networks: `ipvlan_docker` (external, IPv6 L3) + `internal_docker` (bridge, 172.20.0.0/16).

## Security — Multi-layer defense

1. **Cloudflare Authenticated Origin Pulls (mTLS)**: Only CF servers with the Origin Pull certificate. Certificate: `cert/cloudflare-origin-pull.pem`
2. **Go rate limiter**: 20 req/10s per IP → 429 → auto-ban 30min
3. **Go IP blocker**: Persistent blocklist → 403
4. **fail2ban in nginx**: calls admin API with `INTERNAL_TOKEN` → blocks real IP
5. **Cloudflare DDoS/WAF** at the edge
6. **htpasswd bcrypt** (`$2y$`): All passwords hashed with bcrypt

## Key Conventions

- **配置字段严格校验**: 修改任何配置（Xray、Nginx、Docker、cliproxy 等）时，必须对照官方源码/文档验证字段名大小写、合法值范围和字段语义，不得凭记忆或猜测填写。验证方法：读取官方 GitHub 仓库的 Go struct JSON tag（如 Xray `infra/conf/` 下的结构体定义）或官方文档。
- **Git**: linear history on main, one commit per feature. Push → SSH pull → `docker compose build` → `docker compose up -d`
- **Never use `RemoteAddr`** for auth — all traffic on Docker bridge looks internal
- **Internal auth**: `INTERNAL_TOKEN` (auto-generated at startup, stored in `.wework/.internal-token`)
- **Secrets**: gitignored `configs/`, `cert/`, `.wework/`. Local `.env` overrides defaults
- **API key distinction**: `api-keys:` = cliproxy access keys (user-facing); `api-key-entries:` = upstream provider keys
- **Xray client ownership**: Non-admin users can only manage their own Xray clients (one per user). Admin sees all clients.