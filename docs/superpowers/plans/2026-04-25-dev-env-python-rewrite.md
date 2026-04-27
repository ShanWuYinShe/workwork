# dev-env Python Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将 `dev/dev.sh` (800行 bash) 重写为 `dev/dev.py` (单文件 Python)，保持全部功能。

**Architecture:** 单文件 Python，argparse 做子命令路由，subprocess 调用 docker compose，ANSI 彩色输出。

**Tech Stack:** Python 3.8+, 标准库 (subprocess/pathlib/json/os/signal/argparse)

---

## 任务总览

| 任务 | 内容 |
|------|------|
| 1 | 写 `dev/dev.py` 框架（常量/日志/信号/argparse） |
| 2 | 写工具函数（load_env/run_compose/wait_healthy/is_running） |
| 3 | 写开发环境命令（start/stop/teardown/restart/reset/shell/status/logs） |
| 4 | 写备份恢复命令（backup/restore/list-backups） |
| 5 | 写 Xray 命令（generate/list-instances/xray-start/xray-stop） |
| 6 | 写测试环境命令（start-test/stop-test/reset-test/test） |
| 7 | 写维护命令（doctor/prune/completion/version） |
| 8 | 本地语法验证 |
| 9 | 同步到服务器，完整端到端测试 |

---

## Task 1: 写 dev.py 框架

**Files:**
- Create: `dev/dev.py`

- [ ] **Step 1: 创建 dev.py 骨架（shebang + 注释 + 导入）**

```python
#!/usr/bin/env python3
"""
dev-env - Development Environment Manager
Version: 4.1.0
"""

import argparse
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Optional
```

- [ ] **Step 2: 常量定义**

```python
SCRIPT_DIR = Path(__file__).parent.resolve()
PROJECT_ROOT = SCRIPT_DIR.parent
COMPOSE_FILE = PROJECT_ROOT / "dev.docker-compose.yml"
TEST_COMPOSE_FILE = SCRIPT_DIR / "dev-test.yml"
BACKUP_DIR = PROJECT_ROOT / ".local" / "backups"
INSTANCES_FILE = PROJECT_ROOT / "instances.conf"
CONFIG_DIR = PROJECT_ROOT / "configs"
TEMPLATES_DIR = PROJECT_ROOT / "templates"
VERSION = "4.1.0"

RED = "\033[0;31m"
GREEN = "\033[0;32m"
YELLOW = "\033[1;33m"
BLUE = "\033[0;34m"
CYAN = "\033[0;36m"
BOLD = "\033[1m"
DIM = "\033[2m"
NC = "\033[0m"
```

- [ ] **Step 3: 彩色输出函数**

```python
def log(msg: str):
    print(f"{GREEN}[DEV]{NC} {msg}")

def info(msg: str):
    print(f"{BLUE}[INFO]{NC} {msg}")

def warn(msg: str):
    print(f"{YELLOW}[WARN]{NC} {msg}")

def error(msg: str):
    print(f"{RED}[ERR]{NC} {msg}", file=sys.stderr)

def die(msg: str):
    error(msg)
    sys.exit(1)

def debug(msg: str):
    if os.environ.get("DEVENV_DEBUG") == "1":
        print(f"{CYAN}[DBG]{NC} {msg}")
```

- [ ] **Step 4: 信号处理**

```python
def _cleanup_sig(signum, frame):
    print(f"\n{YELLOW}收到中断信号，中止操作{NC}")
    sys.exit(130)

signal.signal(signal.SIGINT, _cleanup_sig)
signal.signal(signal.SIGTERM, _cleanup_sig)
```

- [ ] **Step 5: argparse 骨架**

```python
def main():
    parser = argparse.ArgumentParser(prog="dev.py", description="dev-env manager v4.1.0")
    subparsers = parser.add_subparsers(dest="cmd", required=True)

    subparsers.add_parser("start", help="Start dev environment")
    subparsers.add_parser("stop", help="Stop environment")
    subparsers.add_parser("teardown", help="Destroy environment")
    # ... more subcommands added in later tasks ...

    args = parser.parse_args()
    if args.cmd is None:
        parser.print_help()
        sys.exit(0)

    # dispatch via cmd_XXX function lookup
    fn = globals().get(f"cmd_{args.cmd.replace('-', '_')}")
    if fn:
        fn(args)
    else:
        die(f"Unknown command: {args.cmd}")

if __name__ == "__main__":
    main()
```

- [ ] **Step 6: Commit**

```bash
git add dev/dev.py
git commit -m "feat: init dev.py skeleton (constants, logging, argparse)"
```

---

## Task 2: 写工具函数

**Files:**
- Modify: `dev/dev.py`

- [ ] **Step 1: load_env()**

```python
def load_env() -> dict:
    """Load .env file into os.environ and return dict."""
    env_file = PROJECT_ROOT / ".env"
    if not env_file.exists():
        debug(f".env not found, using defaults")
        return {}
    lines = env_file.read_text().splitlines()
    for line in lines:
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            continue
        key, _, value = line.partition("=")
        os.environ[key.strip()] = value.strip()
    return dict(os.environ)
```

- [ ] **Step 2: run_compose() 基础**

```python
def run_compose(*args, check=True, capture=False, env=None) -> subprocess.CompletedProcess:
    """Run docker compose command."""
    cmd = ["docker", "compose", "-f", str(COMPOSE_FILE)] + list(args)
    merged_env = {**os.environ, **(env or {})}
    result = subprocess.run(
        cmd,
        capture_output=capture,
        text=True,
        env=merged_env
    )
    if check and result.returncode != 0:
        error(f"Command failed: {' '.join(cmd)}")
        if result.stderr:
            error(result.stderr.strip())
        sys.exit(1)
    return result
```

- [ ] **Step 3: is_container_running()**

```python
def is_container_running(name: str) -> bool:
    """Check if container is running."""
    result = subprocess.run(
        ["docker", "ps", "--format", "{{.Names}}"],
        capture_output=True, text=True
    )
    return name in result.stdout.splitlines()
```

- [ ] **Step 4: wait_healthy()**

```python
def wait_healthy(container: str, name: str, timeout: int = 60) -> bool:
    """Wait for container to become healthy. Returns True if healthy, False otherwise."""
    if not is_container_running(container):
        print(f"    {RED}✗{NC} {name} (container not running)")
        return False

    print(f"    {DIM}{name}...{NC}", end="", flush=True)
    for i in range(timeout):
        result = subprocess.run(
            ["docker", "inspect", "--format={{.State.Health.Status}}", container],
            capture_output=True, text=True
        )
        status = result.stdout.strip()
        if status == "healthy":
            print(f"\r    {GREEN}✓{NC} {name}")
            return True
        elif status == "unhealthy":
            print(f"\r    {RED}✗{NC} {name} (unhealthy)")
            return False
        time.sleep(1)
    print(f"\r    {RED}✗{NC} {name} (timeout {timeout}s)")
    return False
```

- [ ] **Step 5: Commit**

```bash
git add dev/dev.py
git commit -m "feat: add utility functions (load_env, run_compose, wait_healthy, is_running)"
```

---

## Task 3: 开发环境命令

**Files:**
- Modify: `dev/dev.py`

- [ ] **Step 1: cmd_start**

```python
def cmd_start(args):
    load_env()
    check_deps()

    if not COMPOSE_FILE.exists():
        die(f"Compose file not found: {COMPOSE_FILE}")

    log("Starting dev environment...")
    run_compose("up", "-d")

    failed = 0
    proj = os.environ.get("COMPOSE_PROJECT", "service")
    if not wait_healthy(f"{proj}-postgres", "PostgreSQL", 30): failed += 1
    if not wait_healthy(f"{proj}-redis", "Redis", 20): failed += 1
    if not wait_healthy(f"{proj}-minio", "MinIO", 30): failed += 1

    if failed:
        error(f"{failed} service(s) failed to start")
        sys.exit(1)

    log("Environment ready")
    info("Services:")
    pg_port = os.environ.get("POSTGRES_PORT", "5433")
    rd_port = os.environ.get("REDIS_PORT", "6379")
    api_port = os.environ.get("MINIO_API_PORT", "9002")
    con_port = os.environ.get("MINIO_CONSOLE_PORT", "9003")
    print(f"  PostgreSQL     : localhost:{pg_port}")
    print(f"  Redis          : localhost:{rd_port}")
    print(f"  MinIO API      : localhost:{api_port}")
    print(f"  MinIO Console : localhost:{con_port}")
    print(f"\n{DIM}Enter dev container: dev.py shell{NC}")
```

- [ ] **Step 2: cmd_stop**

```python
def cmd_stop(args):
    load_env()
    result = subprocess.run(
        ["docker", "compose", "-f", str(COMPOSE_FILE), "ps", "-q"],
        capture_output=True, text=True
    )
    if not result.stdout.strip():
        info("No running services")
        return
    log("Stopping environment...")
    run_compose("stop")
    log("Stopped")
```

- [ ] **Step 3: cmd_teardown**

```python
def cmd_teardown(args):
    log("Destroying dev environment...")
    run_compose("down", "-v")
    log("Destroyed (all data deleted)")
```

- [ ] **Step 4: cmd_restart**

```python
def cmd_restart(args):
    load_env()
    svc = args.service
    if svc:
        log(f"Restarting service: {svc}")
        run_compose("restart", svc)
    else:
        log("Restarting all services...")
        run_compose("restart")
    log("Restarted")
```

- [ ] **Step 5: cmd_reset**

```python
def cmd_reset(args):
    load_env()
    log("Resetting service data...")
    failed = 0
    proj = os.environ.get("COMPOSE_PROJECT", "service")

    pg_cmd = ["docker", "compose", "-f", str(COMPOSE_FILE), "exec", "-T",
              "postgres", "psql", "-U", "app", "-d", "app",
              "-c", "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"]
    if subprocess.run(pg_cmd).returncode == 0:
        info("PostgreSQL: reset")
    else:
        warn("PostgreSQL: reset failed (service may not be running)")
        failed += 1

    rd_cmd = ["docker", "compose", "-f", str(COMPOSE_FILE), "exec", "-T",
              "redis", "redis-cli", "FLUSHALL"]
    if subprocess.run(rd_cmd).returncode == 0:
        info("Redis: reset")
    else:
        warn("Redis: reset failed")
        failed += 1

    mx_cmd = ["docker", "compose", "-f", str(COMPOSE_FILE), "exec", "-T",
              "minio", "sh", "-c", "rm -rf /data/*"]
    if subprocess.run(mx_cmd).returncode == 0:
        info("MinIO: reset")
    else:
        warn("MinIO: reset failed")
        failed += 1

    if failed == 0:
        log("Data reset")
    else:
        warn(f"Partial reset ({failed}/3)")
    sys.exit(1 if failed else 0)
```

- [ ] **Step 6: cmd_shell**

```python
def cmd_shell(args):
    load_env()
    proj = os.environ.get("COMPOSE_PROJECT", "service")
    container = f"{proj}-dev"
    if not is_container_running(container):
        die(f"Dev container not running. Run: dev.py start")
    shell = args.shell or "fish"
    subprocess.run(["docker", "exec", "-it", container, shell])
```

- [ ] **Step 7: cmd_status**

```python
def cmd_status(args):
    load_env()
    print()
    info("Dev Environment Status")
    print()
    run_compose("ps", "--format", "table {{.Name}}\t{{.Status}}\t{{.Ports}}")
    print()
    proj = os.environ.get("COMPOSE_PROJECT", "service")
    print(f"{BOLD}Port Mappings:{NC}")
    print(f"  PostgreSQL     : localhost:{os.environ.get('POSTGRES_PORT','5433')}")
    print(f"  Redis          : localhost:{os.environ.get('REDIS_PORT','6379')}")
    print(f"  MinIO API      : localhost:{os.environ.get('MINIO_API_PORT','9002')}")
    print(f"  MinIO Console  : localhost:{os.environ.get('MINIO_CONSOLE_PORT','9003')}")
    print()
    print(f"{BOLD}Container Env Vars:{NC}")
    print(f"  POSTGRES_HOST  = {proj}-postgres")
    print(f"  REDIS_HOST     = {proj}-redis")
    print(f"  MINIO_ENDPOINT = {proj}-minio:9000")
```

- [ ] **Step 8: cmd_logs**

```python
def cmd_logs(args):
    load_env()
    svc = args.service or ""
    tail = args.tail or "50"
    subprocess.run([
        "docker", "compose", "-f", str(COMPOSE_FILE),
        "logs", "-f", "--tail", tail
    ] + ([svc] if svc else []))
```

- [ ] **Step 9: 添加 argparse 子命令**

在 main() 的 subparsers 中添加：
```python
p = subparsers.add_parser("start")
p = subparsers.add_parser("stop")
p = subparsers.add_parser("teardown")
p = subparsers.add_parser("restart")
p_restart = subparsers.add_parser("restart")
p_restart.add_argument("service", nargs="?")
p = subparsers.add_parser("reset")
p = subparsers.add_parser("shell")
p.add_argument("shell", nargs="?", default="fish")
p = subparsers.add_parser("status")
p_logs = subparsers.add_parser("logs")
p_logs.add_argument("service", nargs="?")
p_logs.add_argument("tail", nargs="?", default="50")
```

- [ ] **Step 10: Commit**

```bash
git add dev/dev.py
git commit -m "feat: add dev environment commands (start/stop/teardown/restart/reset/shell/status/logs)"
```

---

## Task 4: 备份恢复命令

**Files:**
- Modify: `dev/dev.py`

- [ ] **Step 1: cmd_backup**

```python
def cmd_backup(args):
    load_env()
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    name = f"backup_{timestamp}"
    BACKUP_DIR.mkdir(parents=True, exist_ok=True)

    log(f"Backing up to: .local/backups/{name}")
    failed = 0
    proj = os.environ.get("COMPOSE_PROJECT", "service")

    pg_dump = BACKUP_DIR / f"{name}_postgres.sql"
    pg_cmd = ["docker", "compose", "-f", str(COMPOSE_FILE), "exec", "-T",
              "postgres", "pg_dump", "-U", "app", "-d", "app"]
    result = subprocess.run(pg_cmd, stdout=open(pg_dump, "w"), capture_output=True)
    if result.returncode == 0:
        info(f"PostgreSQL: {pg_dump.stat().st_size // 1024}KB")
    else:
        warn("PostgreSQL: backup failed")
        pg_dump.unlink(missing_ok=True)
        failed += 1

    rd_vol = f"{proj}_rd_data"
    rd_tar = BACKUP_DIR / f"{name}_redis.tar.gz"
    rd_cmd = ["docker", "run", "--rm",
              "-v", f"{rd_vol}:/data",
              "-v", f"{BACKUP_DIR}:/backup",
              "alpine", "tar", "czf", f"/backup/{name}_redis.tar.gz", "-C", "/data", "."]
    if subprocess.run(rd_cmd).returncode == 0:
        info(f"Redis: {rd_tar.stat().st_size // 1024}KB")
    else:
        warn("Redis: backup failed")
        rd_tar.unlink(missing_ok=True)
        failed += 1

    if failed == 0:
        log(f"Backup complete: {name}")
    else:
        warn(f"Partial backup ({failed}/2)")
    sys.exit(1 if failed else 0)
```

- [ ] **Step 2: cmd_list_backups**

```python
def cmd_list_backups(args):
    load_env()
    if not BACKUP_DIR.exists():
        info("No backups")
        return
    backups = sorted(BACKUP_DIR.glob("backup_*_postgres.sql"), reverse=True)
    if not backups:
        info("No backups")
        return
    print()
    info("Available backups:")
    print()
    for pg in backups:
        ts = pg.name.replace("backup_", "").replace("_postgres.sql", "")
        date = f"{ts[0:4]}-{ts[4:6]}-{ts[6:8]} {ts[9:11]}:{ts[11:13]}:{ts[13:15]}"
        rd = pg.parent / pg.name.replace("_postgres.sql", "_redis.tar.gz")
        pg_size = (pg.stat().st_size // 1024) if pg.exists() else 0
        rd_size = (rd.stat().st_size // 1024) if rd.exists() else 0
        print(f"  {CYAN}{date}{NC}")
        print(f"    PostgreSQL: {pg_size}KB  Redis: {rd_size}KB")
        print(f"    Restore: dev.py restore {pg.parent / ts}")
    print()
```

- [ ] **Step 3: cmd_restore**

```python
def cmd_restore(args):
    load_env()
    target = args.target
    if not target:
        backups = sorted(BACKUP_DIR.glob("backup_*_postgres.sql"), reverse=True)
        if not backups:
            die("No backup found. Run: dev.py list-backups")
        target = str(backups[0].parent / backups[0].name.replace("_postgres.sql", ""))

    info(f"Using backup: {Path(target).name}")
    pg_sql = Path(f"{target}_postgres.sql")
    rd_tar = Path(f"{target}_redis.tar.gz")

    if not pg_sql.exists() and not rd_tar.exists():
        die(f"Backup files not found: {target}")

    failed = 0
    proj = os.environ.get("COMPOSE_PROJECT", "service")

    if pg_sql.exists():
        log("Restoring PostgreSQL...")
        with open(pg_sql) as f:
            result = subprocess.run(
                ["docker", "compose", "-f", str(COMPOSE_FILE), "exec", "-T",
                 "postgres", "psql", "-U", "app", "-d", "app"],
                stdin=f
            )
        if result.returncode == 0:
            info("PostgreSQL: restored")
        else:
            warn("PostgreSQL: restore failed")
            failed += 1

    if rd_tar.exists():
        log("Restoring Redis...")
        rd_vol = f"{proj}_rd_data"
        rd_cmd = ["docker", "run", "--rm",
                  "-v", f"{rd_vol}:/data",
                  "-v", f"{BACKUP_DIR}:/backup",
                  "alpine", "sh", "-c",
                  f"rm -rf /data/* && tar xzf /backup/{rd_tar.name} -C /data"]
        if subprocess.run(rd_cmd).returncode == 0:
            info("Redis: restored")
        else:
            warn("Redis: restore failed")
            failed += 1

    if failed == 0:
        log("Restore complete")
    else:
        warn(f"Partial restore")
    sys.exit(1 if failed else 0)
```

- [ ] **Step 4: 添加 argparse 子命令**

```python
p = subparsers.add_parser("backup")
p = subparsers.add_parser("restore")
p.add_argument("target", nargs="?")
p = subparsers.add_parser("list-backups")
```

- [ ] **Step 5: Commit**

```bash
git add dev/dev.py
git commit -m "feat: add backup/restore commands (backup/restore/list-backups)"
```

---

## Task 5: Xray 命令

**Files:**
- Modify: `dev/dev.py`

- [ ] **Step 1: cmd_generate**

```python
def cmd_generate(args):
    script = PROJECT_ROOT / "scripts" / "generate-instances.py"
    if not script.exists():
        die(f"Generator script not found: {script}")
    log("Generating Xray instance configs...")
    result = subprocess.run(
        ["python3", str(script)],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        error("Generation failed")
        if result.stderr:
            error(result.stderr)
        sys.exit(1)
    log("Generation complete")
```

- [ ] **Step 2: cmd_list_instances**

```python
def cmd_list_instances(args):
    info_file = PROJECT_ROOT / ".local" / "instances.json"
    if not info_file.exists():
        info("No instances generated. Run: dev.py generate")
        return
    data = json.loads(info_file.read_text())
    print()
    info("Xray Proxy Instances:")
    print()
    for inst in data:
        print(f"  {inst['name']}: {inst['xray_name']} ({inst['xray_path']})")
    print()
    print(f"  {DIM}Details: .local/instances.json (keep private){NC}")
```

- [ ] **Step 3: cmd_xray_start**

```python
def cmd_xray_start(args):
    check_deps()
    log("Starting Xray instances...")
    check_net = subprocess.run(
        ["docker", "network", "ls", "--format", "{{.Name}}"],
        capture_output=True, text=True
    )
    if "ipvlan_docker" not in check_net.stdout:
        warn("Network ipvlan_docker not found")
        info("Run setup-networks.sh to create it")
        sys.exit(1)
    result = subprocess.run(
        ["docker", "compose", "up", "-d"],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        error("Failed to start Xray instances")
        sys.exit(1)
    log("Xray instances started")
```

- [ ] **Step 4: cmd_xray_stop**

```python
def cmd_xray_stop(args):
    log("Stopping Xray instances...")
    result = subprocess.run(
        ["docker", "compose", "stop"],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        error("Failed to stop")
        sys.exit(1)
    log("Stopped")
```

- [ ] **Step 5: 添加 argparse 子命令**

```python
p = subparsers.add_parser("generate")
p = subparsers.add_parser("list-instances")
p = subparsers.add_parser("xray-start")
p = subparsers.add_parser("xray-stop")
```

- [ ] **Step 6: Commit**

```bash
git add dev/dev.py
git commit -m "feat: add Xray commands (generate/list-instances/xray-start/xray-stop)"
```

---

## Task 6: 测试环境命令

**Files:**
- Modify: `dev/dev.py`

- [ ] **Step 1: cmd_start_test**

```python
def cmd_start_test(args):
    check_deps()
    log("Starting test environment...")
    run_compose_test("up", "-d")

    failed = 0
    if not wait_healthy("test-postgres", "PostgreSQL", 30): failed += 1
    if not wait_healthy("test-redis", "Redis", 20): failed += 1
    if not wait_healthy("test-minio", "MinIO", 30): failed += 1

    print()
    if failed == 0:
        log("Test environment ready")
    else:
        warn(f"{failed} service(s) failed")
    print("  PostgreSQL : localhost:5434  (test/test123)")
    print("  Redis      : localhost:6380")
    print("  MinIO      : localhost:9004 / 9005")
    sys.exit(1 if failed else 0)

def run_compose_test(*args, check=True, capture=False):
    cmd = ["docker", "compose", "-f", str(TEST_COMPOSE_FILE)] + list(args)
    result = subprocess.run(cmd, capture_output=capture, text=True)
    if check and result.returncode != 0:
        error(f"Command failed: {' '.join(cmd)}")
        sys.exit(1)
    return result
```

- [ ] **Step 2: cmd_stop_test**

```python
def cmd_stop_test(args):
    log("Stopping test environment...")
    run_compose_test("down", "-v")
    log("Cleaned up")
```

- [ ] **Step 3: cmd_reset_test**

```python
def cmd_reset_test(args):
    log("Resetting test data...")
    failed = 0

    pg_cmd = ["docker", "compose", "-f", str(TEST_COMPOSE_FILE), "exec", "-T",
              "postgres", "psql", "-U", "test", "-d", "test",
              "-c", "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"]
    if subprocess.run(pg_cmd).returncode == 0:
        info("PostgreSQL: reset")
    else:
        warn("PostgreSQL: reset failed")
        failed += 1

    rd_cmd = ["docker", "compose", "-f", str(TEST_COMPOSE_FILE), "exec", "-T",
              "redis", "redis-cli", "FLUSHALL"]
    if subprocess.run(rd_cmd).returncode == 0:
        info("Redis: reset")
    else:
        warn("Redis: reset failed")
        failed += 1

    mx_cmd = ["docker", "compose", "-f", str(TEST_COMPOSE_FILE), "exec", "-T",
              "minio", "sh", "-c", "rm -rf /data/*"]
    if subprocess.run(mx_cmd).returncode == 0:
        info("MinIO: reset")
    else:
        warn("MinIO: reset failed")
        failed += 1

    if failed == 0:
        log("Test data reset")
    else:
        warn(f"Partial reset ({failed}/3)")
    sys.exit(1 if failed else 0)
```

- [ ] **Step 4: cmd_test**

```python
def cmd_test(args):
    log("Full test environment reset...")
    cmd_stop_test(args)
    cmd_start_test(args)
```

- [ ] **Step 5: 添加 argparse 子命令**

```python
p = subparsers.add_parser("start-test")
p = subparsers.add_parser("stop-test")
p = subparsers.add_parser("reset-test")
p = subparsers.add_parser("test")
```

- [ ] **Step 6: Commit**

```bash
git add dev/dev.py
git commit -m "feat: add test environment commands (start-test/stop-test/reset-test/test)"
```

---

## Task 7: 维护命令

**Files:**
- Modify: `dev/dev.py`

- [ ] **Step 1: cmd_doctor**

```python
def cmd_doctor(args):
    errors = 0
    print()
    print(f"{BOLD}=== dev-env Doctor ==={NC}")
    print()

    # 1. Dependencies
    print(f"{BOLD}[1] Dependencies{NC}")
    dc = subprocess.run(["docker", "--version"], capture_output=True, text=True)
    if dc.returncode == 0:
        ver = dc.stdout.strip().split()[2].rstrip(",")
        print(f"  {GREEN}✓{NC} docker ({ver})")
    else:
        print(f"  {RED}✗{NC} docker not found")
        errors += 1

    dcp = subprocess.run(["docker", "compose", "version"], capture_output=True, text=True)
    if dcp.returncode == 0:
        print(f"  {GREEN}✓{NC} docker compose")
    else:
        print(f"  {RED}✗{NC} docker compose not found")
        errors += 1

    di = subprocess.run(["docker", "info"], capture_output=True, text=True)
    if di.returncode == 0:
        print(f"  {GREEN}✓{NC} Docker engine running")
    else:
        print(f"  {RED}✗{NC} Docker engine not running")
        errors += 1

    # 2. Config files
    print()
    print(f"{BOLD}[2] Config Files{NC}")
    load_env()
    for f, label in [(COMPOSE_FILE, "dev.docker-compose.yml"), (TEST_COMPOSE_FILE, "dev-test.yml")]:
        if not f.exists():
            print(f"  {RED}✗{NC} {label} (not found)")
            errors += 1
            continue
        result = subprocess.run(
            ["docker", "compose", "-f", str(f), "config", "--quiet"],
            capture_output=True
        )
        if result.returncode == 0:
            print(f"  {GREEN}✓{NC} {label} (valid)")
        else:
            print(f"  {RED}✗{NC} {label} (syntax error)")
            errors += 1

    # 3. .env
    print()
    print(f"{BOLD}[3] Environment{NC}")
    if (PROJECT_ROOT / ".env").exists():
        print(f"  {GREEN}✓{NC} .env exists")
    else:
        print(f"  {YELLOW}⚠{NC} .env not found (using defaults)")

    # 4. Ports
    print()
    print(f"{BOLD}[4] Ports{NC}")
    ports = [
        ("PostgreSQL", os.environ.get("POSTGRES_PORT", "5433")),
        ("Redis", os.environ.get("REDIS_PORT", "6379")),
        ("MinIO", os.environ.get("MINIO_API_PORT", "9002")),
    ]
    for svc, port in ports:
        result = subprocess.run(
            ["bash", "-c", f"timeout 0.5 bash -c 'echo >/dev/tcp/localhost/{port}'"],
            capture_output=True
        )
        if result.returncode == 0:
            print(f"  {YELLOW}⚠{NC} {svc} ({port}) in use")
        else:
            print(f"  {GREEN}✓{NC} {svc} ({port}) available")

    # 5. Services
    print()
    print(f"{BOLD}[5] Running Services{NC}")
    proj = os.environ.get("COMPOSE_PROJECT", "service")
    for svc in [f"{proj}-postgres", f"{proj}-redis", f"{proj}-minio", f"{proj}-dev"]:
        if is_container_running(svc):
            print(f"  {GREEN}✓{NC} {svc} (running)")
        else:
            print(f"  {DIM}-{NC} {svc} (not running)")

    print()
    if errors == 0:
        log("Doctor: no issues found")
    else:
        error(f"Doctor: {errors} issue(s) found")
    sys.exit(1 if errors else 0)
```

- [ ] **Step 2: cmd_prune**

```python
def cmd_prune(args):
    load_env()
    log("Cleaning orphaned resources...")
    count = 0
    proj = os.environ.get("COMPOSE_PROJECT", "service")

    result = subprocess.run(["docker", "volume", "ls", "-q", "--filter", f"name={proj}_"],
                           capture_output=True, text=True)
    for vol in result.stdout.splitlines():
        label = subprocess.run(
            ["docker", "volume", "inspect", "-f", "{{.Labels.com.docker.compose.project}}", vol],
            capture_output=True, text=True
        ).stdout.strip()
        if not label:
            subprocess.run(["docker", "volume", "rm", vol])
            info(f"Removed volume: {vol}")
            count += 1

    result = subprocess.run(["docker", "network", "ls", "-q", "--filter", f"name={proj}_"],
                           capture_output=True, text=True)
    for net in result.stdout.splitlines():
        conn = subprocess.run(
            ["docker", "network", "inspect", "-f", "{{range .Containers}}{{.Name}} {{end}}", net],
            capture_output=True, text=True
        ).stdout.strip()
        if not conn:
            subprocess.run(["docker", "network", "rm", net])
            info(f"Removed network: {net}")
            count += 1

    if count > 0:
        log(f"Cleaned {count} orphaned resource(s)")
    else:
        info("No orphaned resources")
```

- [ ] **Step 3: cmd_completion**

```python
def cmd_completion(args):
    shell = args.shell or "bash"
    if shell == "bash":
        print("""_dev_py_complete() {
    local cur prev words cword
    _init_completion || return
    local cmds="start stop teardown restart reset shell status logs backup restore list-backups generate list-instances xray-start xray-stop start-test stop-test reset-test test doctor prune completion version help"
    local services="postgres redis minio dev"
    local shells="fish bash zsh sh"
    if [[ $cword -eq 1 ]]; then
        COMPREPLY=($(compgen -W "$cmds" -- "$cur"))
    elif [[ "$prev" == "logs" || "$prev" == "restart" ]]; then
        COMPREPLY=($(compgen -W "$services" -- "$cur"))
    elif [[ "$prev" == "shell" ]]; then
        COMPREPLY=($(compgen -W "$shells" -- "$cur"))
    fi
}
complete -F _dev_py_complete dev.py""")
    elif shell == "zsh":
        print("""#compdef dev.py
_dev_py() {
    local -a cmds services shells
    cmds=(start stop teardown restart reset shell status logs backup restore list-backups generate list-instances xray-start xray-stop start-test stop-test reset-test test doctor prune completion version help)
    services=(postgres redis minio dev)
    shells=(fish bash zsh sh)
    _arguments \\
        "1:command:(${cmds})" \\
        "2:subcmd:->subcmd"
    case $words[2] in
        logs|restart) _describe 'service' services ;;
        shell) _describe 'shell' shells ;;
    esac
}
_dev_py""")
    else:
        warn(f"Completion for {shell} not supported (use bash or zsh)")
```

- [ ] **Step 4: cmd_version**

```python
def cmd_version(args):
    print(f"{CYAN}dev-env v{VERSION}{NC}")
```

- [ ] **Step 5: check_deps 函数**

```python
def check_deps():
    """Check required dependencies are available."""
    for cmd in ["docker", "docker-compose"]:
        result = subprocess.run(["which", cmd], capture_output=True)
        if result.returncode != 0:
            die(f"Missing {cmd}, please install")
    result = subprocess.run(["docker", "info"], capture_output=True)
    if result.returncode != 0:
        die("Docker engine not running or permission denied")
```

- [ ] **Step 6: 添加 argparse 子命令**

```python
p = subparsers.add_parser("doctor")
p = subparsers.add_parser("prune")
p_comp = subparsers.add_parser("completion")
p_comp.add_argument("shell", nargs="?", choices=["bash", "zsh"])
p = subparsers.add_parser("version")
p = subparsers.add_parser("help")
```

- [ ] **Step 7: Commit**

```bash
git add dev/dev.py
git commit -m "feat: add maintenance commands (doctor/prune/completion/version)"
```

---

## Task 8: 本地验证

**Files:**
- `dev/dev.py`

- [ ] **Step 1: 语法检查**

```bash
python3 -m py_compile dev/dev.py && echo "SYNTAX OK"
```

- [ ] **Step 2: 导入检查**

```bash
python3 -c "import dev" 2>&1 || echo "IMPORT CHECK"
# Note: will fail because dev.py uses __main__ guard, just check for syntax errors
python3 dev/dev.py --help
```

- [ ] **Step 3: Commit**

```bash
git add dev/dev.py
git commit -m "chore: dev.py syntax validation complete"
```

---

## Task 9: 服务器端到端测试

**Files:**
- Remote: `liyou@66.94.122.55:~/service/`

- [ ] **Step 1: 上传新文件**

```bash
scp dev/dev.py liyou@66.94.122.55:~/service/dev/dev.py
```

- [ ] **Step 2: 测试 doctor**

```bash
ssh liyou@66.94.122.55 "cd ~/service && python3 dev/dev.py doctor"
```

- [ ] **Step 3: 测试 start/stop**

```bash
ssh liyou@66.94.122.55 "cd ~/service && python3 dev/dev.py start"
ssh liyou@66.94.122.55 "cd ~/service && python3 dev/dev.py status"
ssh liyou@66.94.122.55 "cd ~/service && python3 dev/dev.py stop"
```

- [ ] **Step 4: 测试 generate/start-test**

```bash
ssh liyou@66.94.122.55 "cd ~/service && python3 dev/dev.py generate"
ssh liyou@66.94.122.55 "cd ~/service && python3 dev/dev.py list-instances"
ssh liyou@66.94.122.55 "cd ~/service && python3 dev/dev.py start-test"
ssh liyou@66.94.122.55 "cd ~/service && python3 dev/dev.py stop-test"
```

- [ ] **Step 5: 删除旧 bash 文件**

```bash
ssh liyou@66.94.122.55 "rm ~/service/dev/dev.sh"
```

- [ ] **Step 6: 本地删除并提交**

```bash
rm dev/dev.sh
git add -A
git commit -m "feat: replace bash dev.sh with Python dev.py

- Single-file Python implementation
- argparse-based CLI with simplified command names
- All existing commands preserved: start/stop/teardown/restart/shell/status/logs/backup/restore/generate/xray-start/test environment/doctor/prune
- No external dependencies beyond stdlib"
```

---

## 自检清单

- [ ] spec 覆盖：每个命令都有对应的 task
- [ ] 占位符：无 TBD/TODO/未填写的内容
- [ ] 类型一致：函数签名在所有 task 中一致
- [ ] 命令完整性：bash 版本的所有功能都在 Python 版本中
- [ ] 零外部依赖：只用 Python 标准库
