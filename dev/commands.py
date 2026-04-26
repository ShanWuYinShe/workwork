"""dev-env command implementations."""

import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

from dev.core import (
    UI, NC, load_env, check_deps, is_container_running, wait_healthy,
    run_compose, run_compose_test, compose_args,
    PROJECT_ROOT, COMPOSE_FILE, TEST_COMPOSE_FILE, LOGGING_OVERLAY,
    BACKUP_DIR, SNAPSHOT_DIR, INSTANCES_CONF, INSTANCES_JSON,
    CONFIG_DIR, TEMPLATES_DIR, GENERATOR_SCRIPT,
    ENV_FILE, ENV_TEMPLATE, IP_ALLOC_FILE,
    IPVLAN_NETWORK, IPVLAN_SERVICES, VERSION,
    project_prefix, get_env,
)
from dev.ipam import (
    ensure_ips_allocated, load_ip_allocs, save_ip_allocs,
    ip_env_for_services, detect_ip_conflicts,
    find_free_ip, get_docker_used_ips, get_reserved_ips,
)

# ===========================================
# 1. Development Environment Lifecycle
# ===========================================
def cmd_start(args):
    """Start the development environment."""
    load_env()
    check_deps()

    UI.ok("Allocating IPs...")
    allocs = ensure_ips_allocated()

    issues = detect_ip_conflicts(allocs)
    if issues:
        UI.warn("IP conflicts detected:")
        for i in issues:
            UI.warn(f"  {i}")
        # Disconnect stale containers
        for ip in set(allocs.values()):
            result = subprocess.run(
                ["docker", "network", "inspect", "-f",
                 "{{range .Containers}}{{.Name}}={{.IPv4Address}} {{end}}",
                 IPVLAN_NETWORK],
                capture_output=True, text=True
            )
            for entry in result.stdout.split():
                if "=" in entry and entry.split("=")[1].startswith(ip):
                    cn = entry.split("=")[0]
                    subprocess.run(["docker", "network", "disconnect", "-f", IPVLAN_NETWORK, cn])
                    UI.info(f"  Disconnected {cn} ({ip})")

    ip_env = ip_env_for_services()

    UI.ok("Starting dev environment...")
    run_compose("up", "-d", env=ip_env)

    failed = 0
    prefix = project_prefix()
    if not wait_healthy(f"{prefix}-postgres", "PostgreSQL", 30):
        failed += 1
    if not wait_healthy(f"{prefix}-redis", "Redis", 20):
        failed += 1
    if not wait_healthy(f"{prefix}-minio", "MinIO", 30):
        failed += 1

    if failed:
        UI.err(f"{failed} service(s) failed to start")
        sys.exit(1)

    UI.ok("Environment ready")
    UI.info("Services:")
    print(f"  PostgreSQL     : localhost:{get_env('POSTGRES_PORT', '5433')}  ({allocs.get('postgres', 'N/A')})")
    print(f"  Redis          : localhost:{get_env('REDIS_PORT', '6379')}  ({allocs.get('redis', 'N/A')})")
    print(f"  MinIO API      : localhost:{get_env('MINIO_API_PORT', '9002')}  ({allocs.get('minio', 'N/A')})")
    print(f"  MinIO Console  : localhost:{get_env('MINIO_CONSOLE_PORT', '9003')}")
    print(f"\n{UI.DIM}Enter dev container: dev.py shell{NC}")


def cmd_stop(args):
    """Stop the development environment."""
    load_env()
    if getattr(args, 'all', False):
        result = subprocess.run(
            ["docker", "ps", "--format", "{{.Names}}", "--filter", "label=dev.env.type"],
            capture_output=True, text=True
        )
        containers = [c for c in result.stdout.splitlines() if c.startswith("service-")]
        if not containers:
            UI.info("No dev services running")
            return
        UI.ok(f"Stopping {len(containers)} service(s)...")
        for c in containers:
            subprocess.run(["docker", "stop", c])
        UI.ok("All stopped")
        return

    result = subprocess.run(
        ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + ["ps", "-q"],
        capture_output=True, text=True
    )
    if not result.stdout.strip():
        UI.info("No running services")
        return
    UI.ok("Stopping environment...")
    run_compose("stop")
    UI.ok("Stopped")


def cmd_teardown(args):
    """Destroy the development environment and all data."""
    UI.ok("Destroying dev environment...")
    run_compose("down", "-v")
    UI.ok("Destroyed (all data deleted)")


def cmd_restart(args):
    """Restart one or all services."""
    load_env()
    svc = getattr(args, 'service', None)
    if svc:
        UI.ok(f"Restarting: {svc}")
        run_compose("restart", svc)
    else:
        UI.ok("Restarting all services...")
        run_compose("restart")
    UI.ok("Restarted")


def cmd_reset(args):
    """Reset all service data."""
    load_env()
    UI.ok("Resetting service data...")
    prefix = project_prefix()
    failed = 0

    if subprocess.run([
        "docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
        "exec", "-T", "postgres", "psql", "-U", get_env("POSTGRES_USER", "app"),
        "-d", get_env("POSTGRES_DB", "app"),
        "-c", "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
    ]).returncode == 0:
        UI.info("PostgreSQL: reset")
    else:
        UI.warn("PostgreSQL: reset failed")
        failed += 1

    if subprocess.run([
        "docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
        "exec", "-T", "redis", "redis-cli", "FLUSHALL"
    ]).returncode == 0:
        UI.info("Redis: reset")
    else:
        UI.warn("Redis: reset failed")
        failed += 1

    if subprocess.run([
        "docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
        "exec", "-T", "minio", "sh", "-c", "rm -rf /data/*"
    ]).returncode == 0:
        UI.info("MinIO: reset")
    else:
        UI.warn("MinIO: reset failed")
        failed += 1

    if failed == 0:
        UI.ok("Data reset complete")
    else:
        UI.warn(f"Partial reset ({failed}/3)")
    sys.exit(1 if failed else 0)


def cmd_shell(args):
    """Open a shell inside the dev container."""
    load_env()
    prefix = project_prefix()
    container = f"{prefix}-dev"
    if not is_container_running(container):
        UI.die("Dev container not running. Run: dev.py start")
    shell = getattr(args, 'shell', None) or "fish"
    subprocess.run(["docker", "exec", "-it", container, shell])


def cmd_status(args):
    """Show environment status."""
    load_env()
    print()
    UI.info("Dev Environment Status")
    print()
    run_compose("ps", "--format", "table {{.Name}}\t{{.Status}}\t{{.Ports}}")
    print()
    prefix = project_prefix()
    print(f"{UI.BOLD}Port Mappings:{NC}")
    print(f"  PostgreSQL     : localhost:{get_env('POSTGRES_PORT', '5433')}")
    print(f"  Redis          : localhost:{get_env('REDIS_PORT', '6379')}")
    print(f"  MinIO API      : localhost:{get_env('MINIO_API_PORT', '9002')}")
    print(f"  MinIO Console  : localhost:{get_env('MINIO_CONSOLE_PORT', '9003')}")
    print()
    print(f"{UI.BOLD}Container Env Vars:{NC}")
    print(f"  POSTGRES_HOST  = {prefix}-postgres")
    print(f"  REDIS_HOST     = {prefix}-redis")
    print(f"  MINIO_ENDPOINT = {prefix}-minio:9000")


def cmd_logs(args):
    """Tail logs from services."""
    load_env()
    svc = getattr(args, 'service', None) or ""
    tail = getattr(args, 'tail', None) or "50"
    cmd = ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + ["logs", "-f", "--tail", tail]
    if svc:
        cmd.append(svc)
    subprocess.run(cmd)


def cmd_wait(args):
    """Wait for all services to become healthy."""
    load_env()
    prefix = project_prefix()
    timeout = int(getattr(args, 'timeout', 60))
    UI.ok("Waiting for services...")
    failed = 0
    if not wait_healthy(f"{prefix}-postgres", "PostgreSQL", timeout):
        failed += 1
    if not wait_healthy(f"{prefix}-redis", "Redis", 20):
        failed += 1
    if not wait_healthy(f"{prefix}-minio", "MinIO", 30):
        failed += 1
    if failed:
        UI.warn(f"{failed} service(s) not healthy")
        sys.exit(1)
    UI.ok("All services healthy")


def cmd_exec(args):
    """Run a command inside the dev container."""
    load_env()
    prefix = project_prefix()
    container = f"{prefix}-dev"
    if not is_container_running(container):
        UI.die("Dev container not running. Run: dev.py start")
    exec_args = ["docker", "exec"]
    if sys.stdin.isatty():
        exec_args += ["-it", container]
    else:
        exec_args.append(container)
    exec_args += getattr(args, 'args', [])
    sys.exit(subprocess.run(exec_args).returncode)


# ===========================================
# 2. Init
# ===========================================
def cmd_init(args):
    """Initialize the development environment."""
    load_env()
    UI.header("dev-env Init")

    # .env
    if ENV_FILE.exists():
        UI.info(".env already exists")
    else:
        template = PROJECT_ROOT / "dev" / ".env.template"
        if template.exists():
            shutil.copy2(template, ENV_FILE)
            UI.ok(".env created from template — edit it to customize")
        else:
            UI.warn(".env.template not found, skipping")

    # Network
    result = subprocess.run(
        ["docker", "network", "ls", "--format", "{{.Name}}"],
        capture_output=True, text=True
    )
    if IPVLAN_NETWORK in result.stdout:
        UI.ok(f"Network {IPVLAN_NETWORK} exists")
    else:
        UI.warn(f"Network {IPVLAN_NETWORK} not found")
        UI.info("Run: sudo bash setup-networks.sh")

    # Certs
    cert_dir = PROJECT_ROOT / "cert"
    if cert_dir.exists() and any(cert_dir.iterdir()):
        UI.ok(f"Certs found in cert/")
    else:
        UI.warn("No TLS certificates found in cert/")
        UI.info("Place server_cert.pem and server_cert.key in cert/")

    # Dependencies
    check_deps()
    UI.ok("Docker ready")

    UI.ok("Initialization complete")


# ===========================================
# 3. Backup & Restore
# ===========================================
def cmd_backup(args):
    """Backup PostgreSQL and Redis data."""
    load_env()
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    name = f"backup_{timestamp}"
    BACKUP_DIR.mkdir(parents=True, exist_ok=True)
    prefix = project_prefix()

    UI.ok(f"Backing up to: .local/backups/{name}")
    failed = 0

    pg_file = BACKUP_DIR / f"{name}_postgres.sql"
    pg_cmd = ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
        "exec", "-T", "postgres", "pg_dump",
        "-U", get_env("POSTGRES_USER", "app"), "-d", get_env("POSTGRES_DB", "app")
    ]
    with open(pg_file, "w") as f:
        result = subprocess.run(pg_cmd, stdout=f)
    if result.returncode == 0:
        UI.info(f"PostgreSQL: {pg_file.stat().st_size // 1024}KB")
    else:
        UI.warn("PostgreSQL: backup failed")
        pg_file.unlink(missing_ok=True)
        failed += 1

    rd_vol = f"{prefix}_rd_data"
    rd_file = BACKUP_DIR / f"{name}_redis.tar.gz"
    rd_cmd = [
        "docker", "run", "--rm",
        "-v", f"{rd_vol}:/data",
        "-v", f"{BACKUP_DIR}:/backup",
        "alpine", "tar", "czf", f"/backup/{name}_redis.tar.gz", "-C", "/data", "."
    ]
    if subprocess.run(rd_cmd).returncode == 0:
        UI.info(f"Redis: {rd_file.stat().st_size // 1024}KB")
    else:
        UI.warn("Redis: backup failed")
        rd_file.unlink(missing_ok=True)
        failed += 1

    if failed == 0:
        UI.ok(f"Backup complete: {name}")
    else:
        UI.warn(f"Partial backup ({failed}/2)")
    sys.exit(1 if failed else 0)


def cmd_list_backups(args):
    """List available backups."""
    load_env()
    if not BACKUP_DIR.exists():
        UI.info("No backups")
        return
    backups = sorted(BACKUP_DIR.glob("backup_*_postgres.sql"), reverse=True)
    if not backups:
        UI.info("No backups")
        return
    print()
    UI.info("Available backups:")
    print()
    for pg in backups:
        ts = pg.name.replace("backup_", "").replace("_postgres.sql", "")
        date = f"{ts[0:4]}-{ts[4:6]}-{ts[6:8]} {ts[9:11]}:{ts[11:13]}:{ts[13:15]}"
        rd = pg.parent / pg.name.replace("_postgres.sql", "_redis.tar.gz")
        pg_size = (pg.stat().st_size // 1024) if pg.exists() else 0
        rd_size = (rd.stat().st_size // 1024) if rd.exists() else 0
        print(f"  {UI.CYAN}{date}{NC}")
        print(f"    PostgreSQL: {pg_size}KB  Redis: {rd_size}KB")
        print(f"    Restore: dev.py restore {pg.parent / ts}")
    print()


def cmd_restore(args):
    """Restore from a backup."""
    load_env()
    target = getattr(args, 'target', None)
    if not target:
        backups = sorted(BACKUP_DIR.glob("backup_*_postgres.sql"), reverse=True)
        if not backups:
            UI.die("No backup found. Run: dev.py list-backups")
        target = str(backups[0].parent / backups[0].name.replace("_postgres.sql", ""))

    UI.info(f"Restoring: {Path(target).name}")
    pg_sql = Path(f"{target}_postgres.sql")
    rd_tar = Path(f"{target}_redis.tar.gz")

    if not pg_sql.exists() and not rd_tar.exists():
        UI.die(f"Backup files not found: {target}")

    failed = 0
    if pg_sql.exists():
        UI.ok("Restoring PostgreSQL...")
        with open(pg_sql) as f:
            result = subprocess.run(
                ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
                    "exec", "-T", "postgres", "psql",
                    "-U", get_env("POSTGRES_USER", "app"), "-d", get_env("POSTGRES_DB", "app")
                ], stdin=f
            )
        if result.returncode == 0:
            UI.info("PostgreSQL: restored")
        else:
            UI.warn("PostgreSQL: restore failed")
            failed += 1

    if rd_tar.exists():
        UI.ok("Restoring Redis...")
        prefix = project_prefix()
        rd_vol = f"{prefix}_rd_data"
        result = subprocess.run([
            "docker", "run", "--rm",
            "-v", f"{rd_vol}:/data",
            "-v", f"{BACKUP_DIR}:/backup",
            "alpine", "sh", "-c",
            f"rm -rf /data/* && tar xzf /backup/{rd_tar.name} -C /data"
        ])
        if result.returncode == 0:
            UI.info("Redis: restored")
        else:
            UI.warn("Redis: restore failed")
            failed += 1

    if failed == 0:
        UI.ok("Restore complete")
    else:
        UI.warn("Partial restore")
    sys.exit(1 if failed else 0)


# ===========================================
# 4. Snapshots
# ===========================================
def cmd_snapshot(args):
    """Manage PostgreSQL named snapshots."""
    load_env()
    prefix = project_prefix()
    SNAPSHOT_DIR.mkdir(parents=True, exist_ok=True)
    sub_cmd = getattr(args, 'sub', None)

    if sub_cmd == "list" or sub_cmd is None:
        UI.info("PostgreSQL Snapshots:")
        snaps = sorted(SNAPSHOT_DIR.glob("*.sql"), reverse=True)
        if not snaps:
            print(f"  {UI.DIM}No snapshots (dev.py snapshot create <name>){NC}")
        else:
            for s in snaps:
                size = s.stat().st_size // 1024
                mtime = time.strftime('%Y-%m-%d %H:%M:%S', time.localtime(s.stat().st_mtime))
                print(f"  {UI.CYAN}{s.stem}{NC}  {size}KB  {UI.DIM}{mtime}{NC}")
        return

    if sub_cmd == "create":
        name = getattr(args, 'name', None) or time.strftime("%Y%m%d_%H%M%S")
        snap_file = SNAPSHOT_DIR / f"{name}.sql"
        UI.ok(f"Creating snapshot: {name}")
        result = subprocess.run(
            ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
                "exec", "-T", "postgres", "pg_dump",
                "-U", get_env("POSTGRES_USER", "app"), "-d", get_env("POSTGRES_DB", "app")
            ], stdout=open(snap_file, "w")
        )
        if result.returncode == 0:
            UI.ok(f"Snapshot: {name} ({snap_file.stat().st_size // 1024}KB)")
        else:
            UI.err("Snapshot failed")
            snap_file.unlink(missing_ok=True)
            sys.exit(1)
        return

    if sub_cmd == "restore":
        name = getattr(args, 'name', "latest")
        if name == "latest":
            snaps = sorted(SNAPSHOT_DIR.glob("*.sql"), reverse=True)
            if not snaps:
                UI.die("No snapshots found")
            name = snaps[0].stem
        snap_file = SNAPSHOT_DIR / f"{name}.sql"
        if not snap_file.exists():
            UI.die(f"Snapshot not found: {name}")
        UI.info(f"Restoring snapshot: {name}")
        with open(snap_file) as f:
            result = subprocess.run(
                ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
                    "exec", "-T", "postgres", "psql",
                    "-U", get_env("POSTGRES_USER", "app"), "-d", get_env("POSTGRES_DB", "app")
                ], stdin=f
            )
        if result.returncode == 0:
            UI.ok("Snapshot restored")
        else:
            UI.err("Restore failed")
            sys.exit(1)
        return

    if sub_cmd == "delete":
        name = getattr(args, 'name', None)
        if not name:
            UI.die("Usage: dev.py snapshot delete <name>")
        snap_file = SNAPSHOT_DIR / f"{name}.sql"
        if not snap_file.exists():
            UI.die(f"Snapshot not found: {name}")
        snap_file.unlink()
        UI.ok(f"Deleted: {name}")


# ===========================================
# 5. Xray Proxy
# ===========================================
def cmd_generate(args):
    """Generate Xray configs from templates."""
    if not GENERATOR_SCRIPT.exists():
        UI.die(f"Generator script not found: {GENERATOR_SCRIPT}")
    UI.ok("Generating Xray instance configs...")
    result = subprocess.run(
        ["python3", str(GENERATOR_SCRIPT)],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        UI.err("Generation failed")
        sys.exit(1)
    UI.ok("Generation complete")


def cmd_list_instances(args):
    """List configured Xray instances."""
    if not INSTANCES_JSON.exists():
        UI.info("No instances generated. Run: dev.py generate")
        return
    data = json.loads(INSTANCES_JSON.read_text())
    print()
    UI.info("Xray Proxy Instances:")
    print()
    for inst in data:
        print(f"  {inst['name']}: {inst['xray_name']} ({inst['xray_path']})")
    print()
    print(f"  {UI.DIM}Details: .local/instances.json (keep private){NC}")


def cmd_xray_start(args):
    """Start Xray proxy services."""
    check_deps()
    result = subprocess.run(
        ["docker", "network", "ls", "--format", "{{.Name}}"],
        capture_output=True, text=True
    )
    if IPVLAN_NETWORK not in result.stdout:
        UI.warn(f"Network {IPVLAN_NETWORK} not found")
        UI.info("Run: sudo bash setup-networks.sh")
        sys.exit(1)
    UI.ok("Starting Xray instances...")
    result = subprocess.run(
        ["docker", "compose", "up", "-d"],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        UI.err("Failed to start")
        sys.exit(1)
    UI.ok("Xray instances started")


def cmd_xray_stop(args):
    """Stop Xray proxy services."""
    UI.ok("Stopping Xray instances...")
    result = subprocess.run(
        ["docker", "compose", "stop"],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        UI.err("Failed to stop")
        sys.exit(1)
    UI.ok("Stopped")


# ===========================================
# 6. Test Environment
# ===========================================
def cmd_start_test(args):
    """Start isolated test environment."""
    check_deps()
    UI.ok("Starting test environment...")
    run_compose_test("up", "-d")

    failed = 0
    if not wait_healthy("test-postgres", "PostgreSQL", 30):
        failed += 1
    if not wait_healthy("test-redis", "Redis", 20):
        failed += 1
    if not wait_healthy("test-minio", "MinIO", 30):
        failed += 1

    print()
    if failed == 0:
        UI.ok("Test environment ready")
    else:
        UI.warn(f"{failed} service(s) failed")
    print("  PostgreSQL : localhost:5434  (test/test123)")
    print("  Redis      : localhost:6380")
    print("  MinIO      : localhost:9004 / 9005")
    sys.exit(1 if failed else 0)


def cmd_stop_test(args):
    """Stop and destroy test environment."""
    UI.ok("Stopping test environment...")
    run_compose_test("down", "-v")
    UI.ok("Cleaned up")


def cmd_reset_test(args):
    """Reset test service data."""
    UI.ok("Resetting test data...")
    failed = 0

    if subprocess.run([
        "docker", "compose", "-f", str(TEST_COMPOSE_FILE),
        "exec", "-T", "postgres", "psql", "-U", "test", "-d", "test",
        "-c", "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
    ]).returncode == 0:
        UI.info("PostgreSQL: reset")
    else:
        UI.warn("PostgreSQL: reset failed")
        failed += 1

    if subprocess.run([
        "docker", "compose", "-f", str(TEST_COMPOSE_FILE),
        "exec", "-T", "redis", "redis-cli", "FLUSHALL"
    ]).returncode == 0:
        UI.info("Redis: reset")
    else:
        UI.warn("Redis: reset failed")
        failed += 1

    if subprocess.run([
        "docker", "compose", "-f", str(TEST_COMPOSE_FILE),
        "exec", "-T", "minio", "sh", "-c", "rm -rf /data/*"
    ]).returncode == 0:
        UI.info("MinIO: reset")
    else:
        UI.warn("MinIO: reset failed")
        failed += 1

    if failed == 0:
        UI.ok("Test data reset")
    else:
        UI.warn("Partial reset")
    sys.exit(1 if failed else 0)


def cmd_test(args):
    """Full test environment teardown and restart."""
    cmd_stop_test(args)
    cmd_start_test(args)


# ===========================================
# 7. Maintenance
# ===========================================
def cmd_doctor(args):
    """Run environment diagnostics."""
    load_env()
    errors = 0

    UI.header("dev-env Doctor")

    # 1. Dependencies
    UI.header("1. Dependencies")
    for cmd_name, label in [("docker --version", "docker"), ("docker compose version", "docker compose")]:
        r = subprocess.run(cmd_name.split(), capture_output=True, text=True)
        if r.returncode == 0:
            print(f"  {UI.GREEN}✓{NC} {label}")
        else:
            print(f"  {UI.RED}✗{NC} {label}")
            errors += 1
    r = subprocess.run(["docker", "info"], capture_output=True)
    if r.returncode == 0:
        print(f"  {UI.GREEN}✓{NC} Docker engine running")
    else:
        print(f"  {UI.RED}✗{NC} Docker engine not running")
        errors += 1

    # 2. Config files
    UI.header("2. Config Files")
    for f, label in [(COMPOSE_FILE, "dev.docker-compose.yml"),
                     (TEST_COMPOSE_FILE, "dev-test.yml")]:
        if not f.exists():
            print(f"  {UI.RED}✗{NC} {label} (not found)")
            errors += 1
            continue
        r = subprocess.run(["docker", "compose", "-f", str(f), "config", "--quiet"],
                          capture_output=True)
        if r.returncode == 0:
            print(f"  {UI.GREEN}✓{NC} {label} (valid)")
        else:
            print(f"  {UI.RED}✗{NC} {label} (syntax error)")
            errors += 1

    # 3. Environment
    UI.header("3. Environment")
    if ENV_FILE.exists():
        print(f"  {UI.GREEN}✓{NC} .env exists")
    else:
        print(f"  {UI.YELLOW}⚠{NC} .env not found (using defaults)")

    # 4. Ports
    UI.header("4. Port Availability")
    ports = [
        ("PostgreSQL", get_env("POSTGRES_PORT", "5433")),
        ("Redis", get_env("REDIS_PORT", "6379")),
        ("MinIO", get_env("MINIO_API_PORT", "9002")),
    ]
    for svc, port in ports:
        r = subprocess.run(
            ["bash", "-c", f"timeout 0.5 bash -c 'echo >/dev/tcp/localhost/{port}' 2>/dev/null || true"],
            capture_output=True
        )
        if r.returncode == 0:
            print(f"  {UI.YELLOW}⚠{NC} {svc} ({port}) in use")
        else:
            print(f"  {UI.GREEN}✓{NC} {svc} ({port}) available")

    # 5. Running services
    UI.header("5. Running Services")
    prefix = project_prefix()
    for svc in [f"{prefix}-postgres", f"{prefix}-redis", f"{prefix}-minio", f"{prefix}-dev"]:
        if is_container_running(svc):
            print(f"  {UI.GREEN}✓{NC} {svc}")
        else:
            print(f"  {UI.DIM}⋅{NC} {svc} (not running)")

    print()
    if errors == 0:
        UI.ok("Doctor: no issues found")
    else:
        UI.err(f"Doctor: {errors} issue(s) found")
    sys.exit(1 if errors else 0)


def cmd_prune(args):
    """Clean orphaned Docker resources."""
    load_env()
    UI.ok("Cleaning orphaned resources...")
    prefix = project_prefix()
    count = 0

    result = subprocess.run(
        ["docker", "volume", "ls", "-q", "--filter", f"name={prefix}_"],
        capture_output=True, text=True
    )
    for vol in result.stdout.splitlines():
        label = subprocess.run(
            ["docker", "volume", "inspect", "-f", "{{.Labels.com.docker.compose.project}}", vol],
            capture_output=True, text=True
        ).stdout.strip()
        if not label:
            subprocess.run(["docker", "volume", "rm", vol])
            UI.info(f"Removed volume: {vol}")
            count += 1

    result = subprocess.run(
        ["docker", "network", "ls", "-q", "--filter", f"name={prefix}_"],
        capture_output=True, text=True
    )
    for net in result.stdout.splitlines():
        conn = subprocess.run(
            ["docker", "network", "inspect", "-f", "{{range .Containers}}{{.Name}} {{end}}", net],
            capture_output=True, text=True
        ).stdout.strip()
        if not conn:
            subprocess.run(["docker", "network", "rm", net])
            UI.info(f"Removed network: {net}")
            count += 1

    if count > 0:
        UI.ok(f"Cleaned {count} resource(s)")
    else:
        UI.info("No orphaned resources")


def cmd_completion(args):
    """Generate shell completion scripts."""
    shell = getattr(args, 'shell', None) or "bash"
    if shell == "bash":
        print('''_dev_py_complete() {
    local cur prev words cword
    _init_completion || return
    local cmds="start stop teardown restart reset shell status logs wait exec init backup restore list-backups snapshot generate list-instances xray-start xray-stop start-test stop-test reset-test test doctor prune completion version ip config port-usage help"
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
complete -F _dev_py_complete dev.py''')
    elif shell == "zsh":
        print('''#compdef dev.py
_dev_py() {
    local -a cmds services shells
    cmds=(start stop teardown restart reset shell status logs wait exec init backup restore list-backups snapshot generate list-instances xray-start xray-stop start-test stop-test reset-test test doctor prune completion version ip config port-usage help)
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
_dev_py''')
    else:
        UI.warn(f"Completion for {shell} not supported (use bash or zsh)")


def cmd_version(args):
    """Show version."""
    print(f"{UI.CYAN}dev-env v{VERSION}{NC}")


def cmd_port_usage(args):
    """Show which process is using a port."""
    load_env()
    port = getattr(args, 'port', None)
    if not port:
        UI.info("Common port usage:")
        common = [
            (get_env("POSTGRES_PORT", "5433"), "PostgreSQL"),
            (get_env("REDIS_PORT", "6379"), "Redis"),
            (get_env("MINIO_API_PORT", "9002"), "MinIO API"),
            (get_env("MINIO_CONSOLE_PORT", "9003"), "MinIO Console"),
            ("3000", "Dev (Next.js)"),
            ("3001", "Dev (API)"),
            ("8080", "Dev (Go)"),
        ]
        for p, label in common:
            r = subprocess.run(
                ["bash", "-c", f"lsof -i :{p} -sTCP:LISTEN -n -P 2>/dev/null || true"],
                capture_output=True, text=True
            )
            lines = [l for l in r.stdout.strip().splitlines() if not l.startswith("COMMAND")]
            if lines:
                proc = lines[0].split()[0]
                print(f"  {UI.CYAN}{p}{NC}  {label}  {UI.DIM}({proc}){NC}")
            else:
                print(f"  {UI.DIM}{p}  {label}  (available){NC}")
        return

    r = subprocess.run(
        ["bash", "-c", f"lsof -i :{port} -sTCP:LISTEN -n -P 2>/dev/null || true"],
        capture_output=True, text=True
    )
    if r.stdout.strip():
        UI.info(f"Port {port} in use:")
        for line in r.stdout.strip().splitlines():
            parts = line.split()
            if len(parts) >= 2:
                print(f"  {UI.CYAN}{parts[0]:<15}{NC}  PID: {parts[1]}")
    else:
        UI.info(f"Port {port} is available")


# ===========================================
# 8. IP Management
# ===========================================
def cmd_ip(args):
    """Manage IP allocations."""
    load_env()
    sub = getattr(args, 'sub', None)

    if sub == "list" or sub is None:
        UI.info(f"Network: {IPVLAN_NETWORK}")
        reserved = get_reserved_ips()
        if reserved:
            print(f"  Reserved: {', '.join(sorted(reserved))}")
        used_docker = get_docker_used_ips()
        allocs = load_ip_allocs()
        print()
        print(f"{UI.BOLD}Allocations:{NC}")
        if not allocs:
            print(f"  {UI.DIM}No allocations (run dev.py start){NC}")
        else:
            for svc in IPVLAN_SERVICES:
                ip = allocs.get(svc, "")
                status = ""
                if svc in used_docker:
                    status = f"{UI.GREEN} (in use){NC}"
                elif ip:
                    status = f"{UI.YELLOW} (unused){NC}"
                mark = f"{UI.GREEN}*{NC}" if ip in used_docker else f"{UI.DIM} {NC}"
                print(f"  {mark} {svc:<12} {ip or '(unassigned)'} {status}")

    elif sub == "alloc":
        svc = getattr(args, 'service', None)
        ip = getattr(args, 'ip', None)
        if not svc:
            UI.die("Usage: dev.py ip alloc <service> [ip]")
        if svc not in IPVLAN_SERVICES:
            UI.die(f"Unknown: {svc}. Options: {', '.join(IPVLAN_SERVICES)}")
        allocs = load_ip_allocs()
        if ip:
            used = get_docker_used_ips() | get_reserved_ips()
            if ip in used:
                UI.die(f"IP {ip} already in use or reserved")
        else:
            ip = find_free_ip()
            if not ip:
                UI.die("No free IPs available")
        allocs[svc] = ip
        save_ip_allocs(allocs)
        UI.ok(f"Allocated {ip} to {svc}")

    elif sub == "free":
        svc = getattr(args, 'service', None)
        if not svc:
            UI.die("Usage: dev.py ip free <service>")
        allocs = load_ip_allocs()
        if svc not in allocs:
            UI.die(f"{svc} has no allocated IP")
        ip = allocs.pop(svc)
        save_ip_allocs(allocs)
        UI.ok(f"Freed {ip} from {svc}")

    elif sub == "used":
        UI.info(f"IPs in use on {IPVLAN_NETWORK}:")
        used_ips = get_docker_used_ips()
        if not used_ips:
            print(f"  {UI.DIM}No containers connected{NC}")
        else:
            result = subprocess.run(
                ["docker", "network", "inspect", "-f",
                 "{{range .Containers}}{{.Name}}={{.IPv4Address}} {{end}}", IPVLAN_NETWORK],
                capture_output=True, text=True
            )
            for entry in result.stdout.split():
                if "=" in entry:
                    name, addr = entry.split("=", 1)
                    ip = addr.split("/")[0]
                    print(f"  {name:<30} {ip}")
    else:
        UI.die(f"Unknown subcommand: {sub}")


# ===========================================
# 9. Configuration
# ===========================================
def cmd_config(args):
    """View or edit dev-env configuration."""
    load_env()
    sub = getattr(args, 'sub', None)

    if sub == "show" or sub is None:
        UI.info("Current Configuration:")
        print()
        print(f"  {UI.BOLD}Project:{NC} {PROJECT_ROOT.name}")
        print(f"  {UI.BOLD}Root:{NC}   {PROJECT_ROOT}")
        print(f"  {UI.BOLD}Network:{NC} {IPVLAN_NETWORK}")
        print(f"  {UI.BOLD}Reserved:{NC} {get_env('IPVLAN_RESERVED', '(none)')}")
        print()
        print(f"  {UI.BOLD}Ports:{NC}")
        print(f"    PostgreSQL  : {get_env('POSTGRES_PORT', '5433')}")
        print(f"    Redis       : {get_env('REDIS_PORT', '6379')}")
        print(f"    MinIO API   : {get_env('MINIO_API_PORT', '9002')}")
        print(f"    MinIO Console: {get_env('MINIO_CONSOLE_PORT', '9003')}")
        print()
        print(f"  {UI.BOLD}Databases:{NC}")
        print(f"    PostgreSQL  : {get_env('POSTGRES_DB', 'app')} / {get_env('POSTGRES_USER', 'app')}")
        print(f"    MinIO       : {get_env('MINIO_USER', 'minioadmin')}")
        print()
        print(f"  {UI.BOLD}Files:{NC}")
        print(f"    Compose     : {COMPOSE_FILE}")
        print(f"    .env        : {ENV_FILE}")
        print(f"    IP allocs   : {IP_ALLOC_FILE}")
        print(f"    Backups     : {BACKUP_DIR}")
        return

    if sub == "edit":
        editor = os.environ.get("EDITOR", "vi")
        subprocess.run([editor, str(ENV_FILE)])
        return

    if sub == "ip":
        allocs = load_ip_allocs()
        UI.info("IP Allocations:")
        for svc in IPVLAN_SERVICES:
            ip = allocs.get(svc, "(unassigned)")
            used_ips = get_docker_used_ips()
            status = f"{UI.GREEN}(in use){NC}" if ip in used_ips else ""
            print(f"  {svc:<10} {ip}  {status}")


# ===========================================
# Dashboard
# ===========================================
def cmd_dashboard(args=None):
    """Interactive dashboard shown when dev.py is run with no args."""
    load_env()
    prefix = project_prefix()
    allocs = load_ip_allocs()

    print()
    print(f"{UI.BOLD}{UI.CYAN}dev-env v{VERSION}{NC}  {UI.DIM}{PROJECT_ROOT.name}{NC}")
    print()

    running = []
    stopped = []
    for svc in IPVLAN_SERVICES:
        cname = f"{prefix}-{svc}"
        if is_container_running(cname):
            running.append((svc, cname, allocs.get(svc, "N/A")))
        else:
            stopped.append((svc, cname))

    if running:
        print(f"  {UI.BOLD}{UI.GREEN}● Running{NC}")
        for svc, cname, ip in running:
            print(f"    {UI.GREEN}✓{NC} {svc:<10} {cname}  {UI.DIM}{ip}{NC}")
    else:
        print(f"  {UI.DIM}  No services running (dev.py start){NC}")

    if stopped:
        print(f"  {UI.BOLD}{UI.RED}● Stopped{NC}")
        for svc, cname in stopped:
            print(f"    {UI.RED}⋅{NC} {svc:<10} {cname}")

    print()
    print(f"  {UI.BOLD}Commands:{NC}")
    print(f"    {UI.DIM}start   stop   restart   shell   logs   status{NC}")
    print(f"    {UI.DIM}doctor  ip     backup    init    version{NC}")
    print()
