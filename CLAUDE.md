# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

dev-env is a Docker development environment manager. A Python CLI (`dev/dev.py`) orchestrates Docker Compose services (PostgreSQL, Redis, MinIO, dev container) over an IPvlan L3 network, plus Xray VLESS Reality proxy services. Version 5.0.0.

## Commands

```bash
# Run CLI
python3 dev/dev.py <command>

# Run tests (all)
python3 -m pytest tests/ -v

# Run a single test file
python3 -m pytest tests/test_config.py -v

# Run a single test
python3 -m pytest tests/test_ipam.py::TestIPConversion::test_ip_ordering -v

# No separate build/lint step — this is a CLI tool, not a compiled project
# Python 3.9+ is the runtime (macOS system Python is used)
```

## Architecture

### Module Dependency Graph (critical for avoiding circular imports)

```
paths.py ──→ config.py ──→ core.py ──→ ipam.py ──→ cmds/*.py
   │              │            │                          │
   └──────────────┴────────────┴──────────────────────────┘
```

- `dev/paths.py` — Pure path constants and VERSION. Zero dependencies. Import from anywhere safely.
- `dev/config.py` — Layered YAML config (`dev.yml` + `.env` + env vars). Imports only from `paths.py`.
- `dev/core.py` — UI class, Docker helpers, environment loading. Imports from `paths.py` and lazily from `config.py`.
- `dev/ipam.py` — IP address management. Imports from `paths.py`, `core.py`, `config.py`.
- `dev/cmds/*.py` — Command implementations. Import from core, config, ipam, paths as needed.
- `dev/cli.py` — Argparse setup + dispatch. Imports all cmd functions, builds parser dynamically.

**Circular import rule**: `paths.py` has no imports. `config.py` imports only `paths.py`. Never make `paths.py` or `config.py` depend on `core.py`.

### Entry Points

- `dev/dev.py` — Main entry: adds project root to sys.path, calls `cli.main()`
- `dev/__main__.py` — Supports `python3 -m dev`
- `dev/Makefile` — Make wrapper that delegates to `dev/dev.py`

### Dual Docker Compose Setup

- `dev.docker-compose.yml` — Dev services (PG, Redis, MinIO, dev container). Used by CLI commands via `run_compose()`.
- `docker-compose.yml` — Xray proxy services. Used directly by `docker compose` in `xray-start`/`xray-stop`.
- `dev/dev-test.yml` — Isolated test environment on bridge network with fixed ports/credentials.
- `dev/logging.yml` — Log rotation overlay merged via `compose_args(LOGGING_OVERLAY)`.

### Configuration System

`dev.yml` (committed defaults) is deep-merged with `.env` overrides and environment variables. Access via `get_config("services.postgres.port", 5433)` with dot-notation paths. Services and network names come from config, not hardcoded.

### Xray Config Generation

`scripts/generate_instances.py` reads `instances.yaml`, generates Xray + Nginx configs from `templates/`, auto-generates UUIDs and X25519 keypairs. It imports from `dev.paths` and `dev.config` (adds project root to sys.path for standalone execution).

## Key Conventions

- All command functions return an exit code (0 = success, 1 = failure). `cli.py` calls `sys.exit(fn(args) or 0)`.
- Alias commands (`up`→`start`, `down`→`stop`, `ps`→`status`) are handled by `_ALIAS_MAP` in `cli.py` dispatch.
- IP allocation is persistent in `.local/ip-allocations.json`. The IPAM system auto-allocates from `network.alloc_start` (default 172.18.0.51), skipping reserved IPs.
- Atomic file writes use hidden temp files (`.{name}.tmp`) followed by rename.
- Generated configs and secrets live in `configs/` and `cert/` (gitignored). Local state lives in `.wework/` (gitignored).
