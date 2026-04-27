"""Environment lifecycle: start, stop, teardown, restart, reset, build."""

import subprocess
import sys

from dev.paths import BACKUP_DIR, COMPOSE_FILE, LOGGING_OVERLAY
from dev.core import (
    UI, NC, load_env, check_deps, is_container_running, wait_healthy,
    run_compose, compose_args, get_ipvlan_services, get_ipvlan_network,
    project_prefix,
)
from dev.config import get_config, validate_config
from dev.ipam import ensure_ips_allocated, ip_env_for_services, detect_ip_conflicts


def cmd_start(args):
    """Start the development environment."""
    load_env()

    issues = validate_config()
    if issues:
        for i in issues:
            UI.warn(f"Config: {i}")
        UI.die("Fix configuration errors before starting")

    check_deps()

    network = get_ipvlan_network()
    UI.ok("Allocating IPs...")
    allocs = ensure_ips_allocated()

    issues = detect_ip_conflicts(allocs)
    if issues:
        UI.warn("IP conflicts detected:")
        for i in issues:
            UI.warn(f"  {i}")
        for ip in set(allocs.values()):
            result = subprocess.run(
                ["docker", "network", "inspect", "-f",
                 "{{range .Containers}}{{.Name}}={{.IPv4Address}} {{end}}",
                 network],
                capture_output=True, text=True
            )
            for entry in result.stdout.split():
                if "=" in entry and entry.split("=")[1].startswith(ip):
                    cn = entry.split("=")[0]
                    subprocess.run(["docker", "network", "disconnect", "-f", network, cn])
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
        return 1

    UI.ok("Environment ready")
    UI.info("Services:")
    print(f"  PostgreSQL     : localhost:{get_config('services.postgres.port', 5433)}  ({allocs.get('postgres', 'N/A')})")
    print(f"  Redis          : localhost:{get_config('services.redis.port', 6379)}  ({allocs.get('redis', 'N/A')})")
    print(f"  MinIO API      : localhost:{get_config('services.minio.api_port', 9002)}  ({allocs.get('minio', 'N/A')})")
    print(f"  MinIO Console  : localhost:{get_config('services.minio.console_port', 9003)}")
    print(f"\n{UI.DIM}Enter dev container: dev.py shell{NC}")
    return 0


def cmd_stop(args):
    """Stop the development environment."""
    load_env()
    if getattr(args, 'all', False):
        result = subprocess.run(
            ["docker", "ps", "--format", "{{.Names}}", "--filter", "label=dev.env.type"],
            capture_output=True, text=True
        )
        prefix = project_prefix()
        containers = [c for c in result.stdout.splitlines() if c.startswith(f"{prefix}-")]
        if not containers:
            UI.info("No dev services running")
            return 0
        UI.ok(f"Stopping {len(containers)} service(s)...")
        for c in containers:
            subprocess.run(["docker", "stop", c])
        UI.ok("All stopped")
        return 0

    result = subprocess.run(
        ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + ["ps", "-q"],
        capture_output=True, text=True
    )
    if not result.stdout.strip():
        UI.info("No running services")
        return 0
    UI.ok("Stopping environment...")
    run_compose("stop")
    UI.ok("Stopped")
    return 0


def cmd_teardown(args):
    """Destroy the development environment and all data."""
    load_env()
    if not getattr(args, 'force', False):
        if sys.stdin.isatty():
            try:
                resp = input(f"{UI.YELLOW}This will delete ALL data. Type 'yes' to confirm: {UI.NC}")
                if resp.strip().lower() != "yes":
                    UI.info("Cancelled")
                    return 0
            except (EOFError, KeyboardInterrupt):
                print()
                UI.info("Cancelled")
                return 0

    UI.ok("Destroying dev environment...")
    run_compose("down", "-v")
    UI.ok("Destroyed (all data deleted)")
    return 0


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
    return 0


def cmd_reset(args):
    """Reset all service data."""
    load_env()
    UI.ok("Resetting service data...")
    failed = 0

    if subprocess.run([
        "docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
        "exec", "-T", "postgres", "psql",
        "-U", get_config("services.postgres.user", "app"),
        "-d", get_config("services.postgres.db", "app"),
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
    return 1 if failed else 0


def cmd_build(args):
    """Rebuild dev container image."""
    load_env()
    UI.ok("Building dev container...")
    run_compose("build", "--no-cache", "dev")
    UI.ok("Build complete")
    return 0
