# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

WorkWork is a Docker-based proxy infrastructure with:
- **Xray** VLESS Reality + XHTTP proxy (dual-stack IPv6/ipvlan + IPv4/bridge)
- **Nginx** reverse proxy (Cloudflare Full Proxy → nginx → admin/xray)
- **Admin panel** (Go backend + Svelte SPA) with user management, IPv6 rotation, cliproxy orchestration, model pool
- **Cliproxy** per-user API proxy containers for AI provider access
- **Python CLI** (`dev/dev.py`) for local Docker development environment

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

# Python CLI
python3 dev/dev.py <command>
python3 -m pytest tests/ -v
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

| Page | Nav visible | Route guard | API endpoints | Role |
|------|-------------|-------------|---------------|------|
| Dashboard | all | all | `/api/admin/dashboard` | viewer+ (returns role-specific data) |
| Services | adminOnly | adminOnly | `/api/admin/services` | admin |
| Clients | all | all | `/api/admin/users` (filtered) | viewer+ |
| Users | adminOnly | adminOnly | `/api/admin/users/admin/*` | admin |
| Security | adminOnly | adminOnly | `/api/admin/security/*` | admin |
| Cliproxy | adminOnly | adminOnly | `/api/admin/cliproxy/*` | admin |
| Settings | adminOnly | adminOnly | `/api/admin/network/*` | admin |
| Model Pool | adminOnly | adminOnly | `/api/admin/model-pool/*` | admin |
| Downloads | all | all | `/api/admin/claude-code/stored` | viewer+ |
| Downloads mgmt | adminOnly* | adminOnly* | `/api/admin/claude-code/download` | admin |

*Downloads page is visible to all but admin-only controls (download/delete) are hidden for viewers.

## Service Architecture

### `services/admin/` — Go admin panel (port 8318, internal)

Module layout: single `main` package, no sub-packages except `handlers/`.

| File | Purpose |
|------|---------|
| `main.go` | HTTP route registration, middleware chain, SPA serving |
| `api.go` | Dashboard, Services, Users (xray CRUD), Network status, IPv6 rotation, DNS sync |
| `cliproxy.go` | Per-user cliproxy containers, config get/set (openai-compat + claude-api-key sections) |
| `modelpool.go` | Public AI model pool: providers config + per-user pool access grants |
| `middleware.go` | `requireRole` auth, `isInternalRequest` (shared secret based), UserInfo in `users.json` |
| `ratelimit.go` | Per-IP rate limiter (20 req/10s → auto-ban 30min), feeds into ipblocker |
| `ipblocker.go` | Persistent IP blocklist middleware, admin API for ban/unban |
| `cloudflare.go` | Cloudflare DNS API (AAAA record upsert/find) |
| `deploy.go` | Startup config generation: xray.json, nginx.conf, cliproxy.yaml, internal token |
| `claude.go` | Claude Code binary download from official CDN with SHA256 verification |
| `handlers/users.go` | Xray client config generation (ordered JSON, client template) |

### `ui/` — Svelte 5 SPA

Built with Vite, served by admin Go binary from `/app/ui` (built into Docker image three-stage Dockerfile: node → golang → alpine).

### `services/xray/` — Xray VLESS proxy

- `config.json.tmpl` / `entrypoint.sh` — IPv4 via bridge, IPv6 via ipvlan
- Inbounds: `xhttp-in` (8447, via Cloudflare) + `reality-in` (443, Reality direct)
- Outbounds: `direct-v6` (UseIPv6), `direct-v4` (UseIPv4), `blocked`
- Clients have `email` field (=owner username) for per-user policy/traffic stats
- `migrateXrayClients()` ensures all clients have email on startup

### `services/nginx/` — Nginx + fail2ban

- Serves admin SPA, proxies `/api/admin/` to admin:8318, `/proxy/query` to xray:8447
- fail2ban watches auth log for 401s, calls admin API with `X-Internal-Token` to block real client IPs (not Cloudflare IPs)
- Cloudflare Full Proxy: `real_ip_header CF-Connecting-IP` extracts real client IP

### IPv6 addressing

- Subnet: `/64` from `IPV6_SUBNET` env var
- Static range: `{prefix}:1::/80` — Docker-assigned IPs (nginx, xray primary)
- Rotating range: `{prefix}:f::/80` — 5th hextet = `000f`, DNS points here
- `xray.yinghuoren.com` → rotating IP (reality uses domain, not raw IP)
- Client config: reality outbound uses `xray.yinghuoren.com`, xhttp uses `yinghuoren.com`

### Cliproxy config format

Provider sections (from `services/cliproxy/config.yaml.tmpl`):
```yaml
api-keys:                    # User-facing keys to ACCESS cliproxy
  - "key-value"              # Format: "- \"value\"" NOT "- key: \"value\""
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

4 services: nginx, xray, cliproxy-public, admin. Networks: `ipvlan_docker` (external, IPv6 L3) + `internal_docker` (bridge, 172.20.0.0/16). Admin build context is project root (builds both Go + UI in multi-stage Dockerfile).

## Security — Multi-layer brute force protection

1. **Go rate limiter** (`ratelimit.go`): 20 requests/10s per real IP (X-Real-IP from CF-Connecting-IP) → 429 → auto-ban 30min
2. **Go IP blocker** (`ipblocker.go`): Persistent blocklist → 403
3. **fail2ban in nginx** → calls admin API with shared `INTERNAL_TOKEN` → admin adds real IP to blocklist
4. **Cloudflare DDoS/WAF** at the edge

## Key Conventions

- Git: linear history on main, one commit per feature. Format: `<type>(<scope>): <subject>`. Push → SSH pull → `docker compose build` → `docker compose up -d`.
- Never use `RemoteAddr` for auth decisions — all traffic on Docker bridge looks internal.
- Internal service-to-service auth uses `INTERNAL_TOKEN` (auto-generated at startup, stored in `.wework/.internal-token`).
- Configs/secrets: gitignored `configs/`, `cert/`, `.wework/`. Local `.env` overrides defaults.
- API keys are NOT the same thing: `api-keys:` = cliproxy access keys (user-facing); `api-key-entries:` = upstream provider keys.
