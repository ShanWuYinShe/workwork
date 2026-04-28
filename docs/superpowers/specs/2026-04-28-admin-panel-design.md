# Service Admin Panel Design

## Overview

A Go-based web admin panel to manage all server services (xray, nginx, cliproxyapi, fail2ban) through `yinghuoren.com/admin/`. Authentication is handled entirely by Cloudflare Access with JWT validation at the application layer.

## Security Architecture

```
User Browser
    ↓ HTTPS
Cloudflare Access (identity verification, policy: allow specific email only)
    ↓ injects Cf-Access-Jwt-Assertion header + CF_Authorization cookie
Nginx (reverse proxy, no auth_basic for /admin/)
    ↓ proxy_pass http://127.0.0.1:8318
Admin Panel (Go binary, validates CF JWT via go-oidc)
```

**No self-built auth system.** Cloudflare Access handles:
- Identity provider (Google/GitHub OAuth, email OTP)
- Access policy (email allowlist)
- Session management (token expiry, re-auth)
- DDoS protection, bot filtering

The admin panel validates the `Cf-Access-Jwt-Assertion` header on every request using CF's public keys. Even if someone discovers the origin IP, they cannot forge this JWT.

### Environment Variables

```env
# Added to .env
CF_TEAM_DOMAIN=https://<team-name>.cloudflareaccess.com
CF_POLICY_AUD=<aud-tag-from-access-app>
ADMIN_PORT=8318
```

## Tech Stack

- **Backend**: Go 1.22+, `net/http` stdlib, `html/template`
- **Docker integration**: Docker Engine API via `docker/client` SDK (unix socket)
- **JWT validation**: `github.com/coreos/go-oidc/v3/oidc`
- **Frontend**: Server-rendered HTML + inline CSS + minimal JavaScript (HTMX for interactivity)
- **No external database**: All state from filesystem (.env, templates, Docker API)

## Docker Service Definition

Added to `docker-compose.yml`:

```yaml
  admin:
    build:
      context: ./services/admin
      dockerfile: Dockerfile
    restart: unless-stopped
    network_mode: host
    environment:
      - CF_TEAM_DOMAIN=${CF_TEAM_DOMAIN}
      - CF_POLICY_AUD=${CF_POLICY_AUD}
      - ADMIN_PORT=${ADMIN_PORT}
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./.env:/app/.env
      - ./services:/app/services
      - ./logs:/app/logs
      - ./deploy.sh:/app/deploy.sh:ro
      - ./cert:/app/cert:ro
    depends_on:
      - nginx
```

Mounts explained:
- `docker.sock`: read-write, for container management (list, restart, logs)
- `.env`: read-only, for reading config values and writing back
- `services/`: read-write, for deploy.sh to regenerate configs
- `logs/`: read-write, for log viewer and audit trail
- `deploy.sh`: read-only, admin calls it to regenerate configs
- `cert/`: read-only, for certificate info display

## Directory Structure

```
services/admin/
├── Dockerfile
├── go.mod
├── go.sum
├── main.go                 # entry point, router setup
├── middleware/
│   └── auth.go             # CF JWT validation middleware
├── handlers/
│   ├── dashboard.go        # GET /admin/
│   ├── services.go         # GET /admin/services, POST /admin/services/{name}/restart
│   ├── logs.go             # GET /admin/services/{name}/logs (SSE streaming)
│   ├── config.go           # GET /admin/config, POST /admin/config (edit .env + deploy)
│   ├── users.go            # GET /admin/users, POST /admin/users/{type}
│   └── monitoring.go       # GET /admin/monitoring (system stats, fail2ban)
└── templates/
    ├── layout.html          # base layout with nav
    ├── dashboard.html
    ├── services.html
    ├── logs.html
    ├── config.html
    ├── users.html
    └── monitoring.html
```

## Routes

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/admin/` | dashboard | Service status overview |
| GET | `/admin/services` | services.List | List all services with status |
| POST | `/admin/services/{name}/start` | services.Start | Start a service |
| POST | `/admin/services/{name}/stop` | services.Stop | Stop a service |
| POST | `/admin/services/{name}/restart` | services.Restart | Restart a service |
| GET | `/admin/services/{name}/logs` | logs.Stream | SSE log streaming |
| GET | `/admin/config` | config.View | Show current .env values |
| POST | `/admin/config` | config.Update | Save .env + run deploy.sh |
| POST | `/admin/config/deploy` | config.Deploy | Re-run deploy.sh only |
| GET | `/admin/users` | users.List | Show all users (xray/cliproxyapi/nginx) |
| POST | `/admin/users/xray` | users.AddXrayUser | Add xray client UUID |
| POST | `/admin/users/xray/{id}` | users.RemoveXrayUser | Remove xray client UUID |
| POST | `/admin/users/cliproxyapi` | users.AddAPIKey | Add cliproxyapi API key |
| DELETE | `/admin/users/cliproxyapi/{key}` | users.RemoveAPIKey | Remove API key |
| POST | `/admin/users/nginx` | users.AddNginxUser | Add htpasswd user |
| DELETE | `/admin/users/nginx/{user}` | users.RemoveNginxUser | Remove htpasswd user |
| GET | `/admin/monitoring` | monitoring.View | System stats + fail2ban |

## Nginx Configuration Change

Add `/admin/` location block in `nginx.conf.tmpl`, no auth_basic:

```nginx
location /admin/ {
    proxy_pass http://127.0.0.1:__ADMIN_PORT__;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header Cf-Access-Jwt-Assertion $http_cf_access_jwt_assertion;
    proxy_set_header Cf-Access-Authenticated-User-Email $http_cf_access_authenticated_user_email;
    proxy_read_timeout 300s;
    proxy_send_timeout 300s;
}
```

Note: `auth_basic off` is implicit (no auth_basic directive = no auth).

## Handler Details

### Dashboard (`/admin/`)
- Call Docker API to list containers, show status (running/stopped/restarting)
- Show quick stats: uptime, memory usage, active connections
- Show recent fail2ban bans (last 10)

### Services (`/admin/services`)
- List: `docker compose ps` equivalent via Docker API
- Start/Stop/Restart: `docker compose start/stop/restart <service>` via Docker API
- Each action logged to `logs/admin-audit.log` with timestamp + user email (from CF JWT)

### Logs (`/admin/services/{name}/logs`)
- Server-Sent Events (SSE) endpoint
- Stream `docker compose logs -f <service>` output
- Client can pause/resume

### Config (`/admin/config`)
- GET: Parse and display `.env` as editable form (passwords masked)
- POST: Validate input, write new `.env`, run `deploy.sh`, optionally restart affected services
- deploy only: re-run `deploy.sh` without editing .env

### Users (`/admin/users`)
- **Xray users**: Read/write `services/xray/config.json`, modify `XRAY_CLIENT_IDS` in `.env`, regenerate config
- **CliproxyAPI keys**: Read/write `CLIPROXY_API_KEYS` in `.env`, regenerate config
- **Nginx users**: Read/write `AUTH_USER`/`AUTH_PASS` in `.env`, or manage `services/nginx/.htpasswd` directly via `htpasswd` binary
- All changes trigger `deploy.sh` to regenerate configs + reload affected services

### Monitoring (`/admin/monitoring`)
- System: CPU, memory, disk usage (via `syscall` / `os` package)
- Docker: per-container CPU/memory/network stats via Docker API
- Fail2ban: run `fail2ban-client status <jail>` via Docker exec, show banned IPs

## Audit Logging

All write operations (restart, config change, user add/remove) are logged to `logs/admin-audit.log`:

```
2026-04-28T15:30:00Z user@example.com RESTART service=xray
2026-04-28T15:31:00Z user@example.com CONFIG_UPDATE key=XRAY_CLIENT_IDS
2026-04-28T15:32:00Z user@example.com USER_ADD type=xray id=uuid-value
```

## Cloudflare Access Setup Steps (manual, one-time)

1. Go to Cloudflare One Dashboard → Access → Applications
2. Add Application → Self-hosted
3. Application name: "Service Admin Panel"
4. Session Duration: 24 hours
5. Add public hostname: `yinghuoren.com/admin/*`
6. Policy: Allow, Email equals `<your-email>`
7. Save → copy AUD Tag to `.env`

## Build & Deploy

```bash
# .env.example additions:
CF_TEAM_DOMAIN=https://<team-name>.cloudflareaccess.com
CF_POLICY_AUD=<aud-tag>
ADMIN_PORT=8318
```

```bash
# deploy.sh additions:
# After nginx.conf generation, add ADMIN_PORT substitution:
#   sed -i "s|__ADMIN_PORT__|${ADMIN_PORT}|g" services/nginx/nginx.conf
# ... existing deploy logic ...
```

The admin panel is built as a Docker image from `services/admin/Dockerfile` (multi-stage Go build).

## Implementation Phases

**Phase 1 — Foundation** (this spec):
- Go project scaffold + Dockerfile
- CF JWT auth middleware
- Dashboard + Services handlers (status, start/stop/restart)
- Log viewer (SSE)
- Nginx route addition

**Phase 2 — Configuration**:
- .env editor
- deploy.sh integration
- Config regeneration + service reload

**Phase 3 — User Management**:
- Xray UUID management
- CliproxyAPI key management
- Nginx htpasswd management

**Phase 4 — Monitoring**:
- System resource monitoring
- Docker stats
- Fail2ban integration

Phase 1 is the minimum viable product. Phases 2-4 can be implemented incrementally.
