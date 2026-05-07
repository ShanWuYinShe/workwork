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
ssh workwork-server "cd service && git pull origin main && docker compose build --no-cache admin && docker compose up -d"

# Python CLI
python3 dev/dev.py <command>
python3 -m pytest tests/ -v
```

## Service Architecture

### `services/admin/` — Go admin panel (port 8318, internal)

Module layout: single `main` package, no sub-packages except `handlers/`.

| File | Purpose |
|------|---------|
| `main.go` | HTTP route registration + SPA serving |
| `api.go` | Dashboard, Services, Users (xray clients CRUD), Network status, IPv6 rotation, DNS sync |
| `cliproxy.go` | Per-user cliproxy container lifecycle, config get/set, Codex OAuth |
| `modelpool.go` | Public AI model pool: providers config + per-user pool access grants |
| `middleware.go` | Auth middleware (`requireRole`), UserInfo (Role/PoolAccess/CliproxyToken) in `users.json` |
| `cloudflare.go` | Cloudflare DNS API (AAAA record upsert/find) |
| `deploy.go` | First-start config generation: xray.json, nginx.conf, cliproxy.yaml from templates |
| `ipblocker.go` | IP block middleware |
| `handlers/users.go` | Xray client config generation (ordered JSON, client template) |

**Role-based access**: `requireRole("admin")` / `requireRole("viewer")` middleware checks `X-Forwarded-User` header (set by nginx basic auth). User roles stored in `/app/data/users.json`. Admin sees all; viewer sees only their own resources.

**IPv6 rotation**: `genRandomIPv6()` generates IPs in `:f::/80` rotating range (5th hextet = `000f`). Rotation adds IP to xray container's ipvlan interface (auto-detected via `disable_ipv6` check), updates Cloudflare DNS, removes old IP. Static IPs live in `:1::/80` range.

**Model pool**: Admin configures shared AI providers (name/base_url/api_key) in `/app/data/model-pool.json`. Per-user `pool_access` toggle grants access. Pool providers are merged into user's cliproxy config with `[Pool]` prefix, auto- regenerating and restarting containers on change.

### `ui/` — Svelte 5 SPA

Built with Vite, served by admin Go binary from `/app/ui` (built into Docker image). Routes: Dashboard, Services (admin), Clients, Users (admin), Security (admin), Cliproxy (admin), Settings (admin), Model Pool (admin).

### `services/xray/` — Xray VLESS proxy

- `config.json.tmpl` — Template with `__XRAY_CLIENTS_JSON__` / `__XRAY_REALITY_CLIENTS_JSON__` placeholders
- `entrypoint.sh` — Routes IPv4 through Docker bridge (`172.20.0.1`), IPv6 through ipvlan. Auto-detects bridge interface by matching `172.20.x.x` subnet.
- Two inbounds: `xhttp-in` (8447, XHTTP+TLS via Cloudflare) + `reality-in` (443, VLESS Reality direct)
- Three outbounds: `direct-v6` (UseIPv6), `direct-v4` (UseIPv4), `blocked`
- Routing: IPv6 traffic → direct-v6, IPv4 → direct-v4

### `services/nginx/` — Nginx reverse proxy

Serves admin panel at `/admin/` and proxies `/api/admin/` to admin:8318, `/proxy/query` to xray:8447. HTTP basic auth via `.htpasswd`. Cloudflare Full Proxy handles TLS.

### `services/cliproxy/` — CLI Proxy API

Per-user containers (`cliproxy-{token}`) created dynamically by admin service. Each mounts a generated `config.yaml` with the user's API providers. Public instance defined in docker-compose.yml.

### `docker-compose.yml`

4 services: nginx, xray, cliproxy-public, admin. Networks: `ipvlan_docker` (external, IPv6 L3) + `internal_docker` (bridge, 172.20.0.0/16). Admin build context is project root (builds both Go + UI in multi-stage Dockerfile).

### Python CLI (`dev/`)

Module chain: `paths.py` → `config.py` → `core.py` → `ipam.py` → `cmds/*.py`. `paths.py` and `config.py` must never import from `core.py`. Manages local dev environment (PostgreSQL, Redis, MinIO, dev container), not the production proxy services.

## Key Conventions

- Git: linear history on main, one commit per feature. Rebase in worktree, merge with `--ff-only`. Format: `<type>(<scope>): <subject>`.
- Deployment: `git push` → SSH server → `git pull` → `docker compose build --no-cache admin` → `docker compose up -d`. The Dockerfile builds both Go and Svelte UI.
- IPv6: static range `:1::/80` (Docker-assigned), rotating range `:f::/80` (dynamically generated). DNS points to rotating IP.
- Configs/secrets: gitignored `configs/`, `cert/`, `.wework/`. Local `.env` overrides `dev.yml` defaults.
