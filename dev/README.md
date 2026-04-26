# dev-env - Development Environment Manager

v4.2.0

Development environment manager for Go + TypeScript projects with PostgreSQL, Redis, MinIO, and Bun (frontend). Manages both development and Xray proxy services.

## Prerequisites

- Docker and Docker Compose plugin
- IPv4/IPv6 network with parent interface (run `bash setup-networks.sh` once)
- TLS certificates for Xray proxy (place in `cert/` directory)

## Quick Start

```bash
python3 dev/dev.py start      # Start development environment
python3 dev/dev.py            # Dashboard (no args)
python3 dev/dev.py shell      # Enter dev container
python3 dev/dev.py stop       # Stop environment
```

## Development Container

Go 1.25 + Node.js 22 + Bun + TypeScript + fish shell.

```bash
python3 dev/dev.py shell          # Enter with fish (default)
python3 dev/dev.py shell bash     # Enter with bash
python3 dev/dev.py exec echo hello  # Run command in container
```

## Commands

### Dashboard

```bash
python3 dev/dev.py          # Show dashboard (services, quick commands)
```

### Development Environment

| Command | Description |
|---------|-------------|
| `start` | Start development environment |
| `stop` | Stop environment |
| `stop --all` | Stop all dev projects |
| `teardown` | Destroy environment (delete all data) |
| `restart [service]` | Restart service(s) |
| `reset` | Reset service data |
| `shell [shell]` | Enter dev container (default: fish) |
| `status` | Show environment status |
| `logs [service]` | View logs (follow, tail 50) |
| `wait [--timeout N]` | Wait for services to become healthy |
| `exec <cmd...>` | Run command in dev container |

### Backup & Restore

| Command | Description |
|---------|-------------|
| `backup` | Backup PostgreSQL and Redis data |
| `restore [name]` | Restore from backup (default: latest) |
| `list-backups` | List available backups |

### PostgreSQL Snapshots

Named snapshots (faster than time-based backups):

```bash
python3 dev/dev.py snapshot list                    # List snapshots
python3 dev/dev.py snapshot create my_feature      # Create snapshot
python3 dev/dev.py snapshot restore my_feature     # Restore snapshot
python3 dev/dev.py snapshot restore latest        # Restore latest
python3 dev/dev.py snapshot delete my_feature      # Delete snapshot
```

### IP Address Management

Services use `ipvlan_docker` network with manually managed IPs:

```bash
python3 dev/dev.py ip list    # Show IP allocations
python3 dev/dev.py ip used    # Show IPs in use by docker
python3 dev/dev.py ip alloc postgres 172.18.0.60   # Manual allocation
python3 dev/dev.py ip alloc redis                 # Auto allocation
python3 dev/dev.py ip free dev                    # Free IP
```

Reserved IP range (in `.env`): `IPVLAN_RESERVED=172.18.0.1-172.18.0.50`

### Xray Proxy Instances

Xray requires a one-time network setup and TLS certificates before first use.

```bash
# 1. Set up ipvlan network (one-time, may require root)
sudo bash setup-networks.sh

# 2. Generate TLS certificates in cert/ directory
#    Place server.pem and server.key in cert/ (used by Nginx for TLS)

# 3. Generate Xray configs
python3 dev/dev.py generate

# 4. Manage Xray services
python3 dev/dev.py xray-start       # Start Xray proxy services
python3 dev/dev.py xray-stop        # Stop Xray proxy services
python3 dev/dev.py list-instances   # List configured instances
```

Adding a new instance:

1. Add entry to `instances.conf`:
   ```
   newuser, xray-reality-newuser, newuser, , , , www.microsoft.com:443, ["www.microsoft.com"]
   ```
2. Run `python3 dev/dev.py generate`
3. Run `python3 dev/dev.py xray-start`

Client info (UUID, private key) is saved to `.local/instances.json` — keep this private.

### Test Environment

| Command | Description |
|---------|-------------|
| `start-test` | Start isolated test environment |
| `stop-test` | Stop and remove test environment |
| `reset-test` | Reset test data |
| `test` | Full reset: teardown → start → wait healthy |

### Configuration

```bash
python3 dev/dev.py config show     # Show current configuration
python3 dev/dev.py config edit     # Edit .env file
python3 dev/dev.py config ip       # Show IP allocations
```

### Diagnostics

```bash
python3 dev/dev.py doctor          # Run diagnostics (checks deps, ports, services)
python3 dev/dev.py port-usage      # Show which process uses common ports
python3 dev/dev.py port-usage 5433 # Show which process uses port 5433
python3 dev/dev.py prune            # Clean orphaned Docker resources
```

### Shell Completion

```bash
python3 dev/dev.py completion bash >> ~/.bashrc   # Install bash completion
python3 dev/dev.py completion zsh >> ~/.zshrc     # Install zsh completion
```

### Other

```bash
python3 dev/dev.py version     # Show version
python3 dev/dev.py help       # Show help
```

## Service Ports

| Service | Default Port |
|---------|-------------|
| PostgreSQL | 5433 |
| Redis | 6379 |
| MinIO API | 9002 |
| MinIO Console | 9003 |
| Dev (app) | 3000 |
| Dev (hot) | 3001 |
| Dev (debug) | 8080 |

## Test Environment Ports

| Service | Port |
|---------|------|
| PostgreSQL | 5434 |
| Redis | 6380 |
| MinIO API | 9004 |
| MinIO Console | 9005 |

## Environment Variables

Configure in `.env`:

| Variable | Default | Description |
|----------|---------|-------------|
| `COMPOSE_PROJECT` | `service` | Docker Compose project name |
| `POSTGRES_DB` | `app` | Database name |
| `POSTGRES_USER` | `app` | Database user |
| `POSTGRES_PASSWORD` | `app123` | Database password |
| `POSTGRES_PORT` | `5433` | Host port |
| `REDIS_PORT` | `6379` | Host port |
| `MINIO_USER` | `minioadmin` | MinIO root user |
| `MINIO_PASSWORD` | `minioadmin123` | MinIO root password |
| `MINIO_API_PORT` | `9002` | S3 API port |
| `MINIO_CONSOLE_PORT` | `9003` | Web console port |
| `IPVLAN_RESERVED` | _(none)_ | Reserved IP range, e.g. `172.18.0.1-172.18.0.50` |

## Dev Container Tools

Go 1.25, Node.js 22, Bun, TypeScript, psql, redis-cli, fish shell.

## Project Structure

```
dev/                        # Development environment manager
  dev.py                    # CLI manager (Python, zero deps)
  dev-test.yml              # Test environment compose
  Dockerfile                # Dev container (node + go + bun)
  Makefile                  # Make wrapper
  .env.template             # Configuration template
  logging.yml               # Log rotation overlay

scripts/
  generate-instances.py     # Xray config generator

templates/
  xray-reality-template.json   # Xray Reality config template
  nginx-xhttp-template.conf     # Nginx XHTTP proxy template

instances.conf          # Xray instance definitions

# Root-level (Xray proxy infrastructure)
docker-compose.yml      # Xray + Nginx services
setup-networks.sh      # One-time network setup
nginx/                  # Nginx proxy Dockerfile + config
cert/                   # TLS certificates (gitignored)
configs/                # Generated Xray configs (gitignored)
.local/                 # Local data: backups, snapshots, IPs (gitignored)
```

## Makefile

```bash
make start               # Start dev environment
make stop                # Stop dev environment
make test                # Test environment
make generate            # Generate Xray configs
make xray-up             # Start Xray proxy services
make doctor              # Diagnostics
```
