"""dev-env core infrastructure: UI, environment, Docker helpers."""

import os
import signal
import subprocess
import sys
import time
from pathlib import Path

from dev.paths import (
    VERSION, COMPOSE_FILE, TEST_COMPOSE_FILE, LOGGING_OVERLAY,
)

# Shorthand color constant for use in f-strings across modules
NC = "\033[0m"

# Global flags set by CLI
QUIET = False
JSON_OUTPUT = False


def _ipvlan_services() -> list[str]:
    """Get service list from config (falls back to hardcoded default)."""
    try:
        from dev.config import get_config
        services = get_config("services")
        if isinstance(services, dict):
            return list(services.keys())
    except Exception:
        pass
    return ["postgres", "redis", "minio", "dev"]


def get_ipvlan_services() -> list[str]:
    """Get configured service names."""
    return _ipvlan_services()


def get_ipvlan_network() -> str:
    """Get configured network name."""
    try:
        from dev.config import get_config
        return get_config("network.name", "ipvlan_docker")
    except Exception:
        return "ipvlan_docker"


# ===========================================
# UI Utilities
# ===========================================
class UI:
    """Colored output helpers."""
    RED = "\033[0;31m"
    GREEN = "\033[0;32m"
    YELLOW = "\033[1;33m"
    BLUE = "\033[0;34m"
    CYAN = "\033[0;36m"
    BOLD = "\033[1m"
    DIM = "\033[2m"
    NC = "\033[0m"

    @classmethod
    def ok(cls, msg: str):
        if not QUIET:
            print(f"{cls.GREEN}[OK]{cls.NC} {msg}")

    @classmethod
    def info(cls, msg: str):
        if not QUIET:
            print(f"{cls.BLUE}[INFO]{cls.NC} {msg}")

    @classmethod
    def warn(cls, msg: str):
        if not QUIET:
            print(f"{cls.YELLOW}[WARN]{cls.NC} {msg}")

    @classmethod
    def err(cls, msg: str):
        print(f"{cls.RED}[ERR]{cls.NC} {msg}", file=sys.stderr)

    @classmethod
    def die(cls, msg: str):
        cls.err(msg)
        sys.exit(1)

    @classmethod
    def step(cls, msg: str):
        if not QUIET:
            print(f"    {msg}...", end="", flush=True)

    @classmethod
    def step_ok(cls, msg: str):
        if not QUIET:
            print(f"\r    {cls.GREEN}✓{cls.NC} {msg}")

    @classmethod
    def step_fail(cls, msg: str):
        if not QUIET:
            print(f"\r    {cls.RED}✗{cls.NC} {msg}")

    @classmethod
    def header(cls, text: str):
        if not QUIET:
            print(f"\n{cls.BOLD}=== {text} ==={cls.NC}\n")


def _cleanup_sig(signum, frame):
    print(f"\n{UI.YELLOW}Interrupted{UI.NC}")
    sys.exit(130)


signal.signal(signal.SIGINT, _cleanup_sig)
signal.signal(signal.SIGTERM, _cleanup_sig)


# ===========================================
# Environment
# ===========================================
def load_env() -> dict:
    """Load configuration and populate os.environ for Docker Compose."""
    from dev.config import load_config, get_config
    load_config()

    mappings = {
        "COMPOSE_PROJECT": get_config("project"),
        "POSTGRES_DB": get_config("services.postgres.db"),
        "POSTGRES_USER": get_config("services.postgres.user"),
        "POSTGRES_PASSWORD": get_config("services.postgres.password"),
        "POSTGRES_PORT": str(get_config("services.postgres.port", "")),
        "REDIS_PORT": str(get_config("services.redis.port", "")),
        "MINIO_USER": get_config("services.minio.user"),
        "MINIO_PASSWORD": get_config("services.minio.password"),
        "MINIO_API_PORT": str(get_config("services.minio.api_port", "")),
        "MINIO_CONSOLE_PORT": str(get_config("services.minio.console_port", "")),
    }
    for var, val in mappings.items():
        if val:
            os.environ[var] = str(val)

    for svc in get_ipvlan_services():
        os.environ.setdefault(f"IPVLAN_IP_{svc.upper()}", "")

    return dict(os.environ)


def project_prefix() -> str:
    from dev.config import get_config
    return get_config("project", "service")


# ===========================================
# Docker Helpers
# ===========================================
def check_deps():
    """Verify docker + compose are installed and running."""
    r1 = subprocess.run(["docker", "--version"], capture_output=True)
    if r1.returncode != 0:
        UI.die("Docker not found. Please install Docker first.")
    r2 = subprocess.run(["docker", "compose", "version"], capture_output=True)
    if r2.returncode != 0:
        UI.die("Docker Compose plugin not found.")
    r3 = subprocess.run(["docker", "info"], capture_output=True)
    if r3.returncode != 0:
        UI.die("Docker engine not running or permission denied.")


def is_container_running(name: str) -> bool:
    result = subprocess.run(
        ["docker", "ps", "--format", "{{.Names}}"],
        capture_output=True, text=True
    )
    return name in result.stdout.splitlines()


def container_health(name: str) -> str:
    """Return health status of container: healthy, unhealthy, starting, or ''."""
    result = subprocess.run(
        ["docker", "inspect", "--format={{.State.Health.Status}}", name],
        capture_output=True, text=True
    )
    return result.stdout.strip()


def container_uptime(name: str) -> str:
    """Return human-readable uptime for container."""
    result = subprocess.run(
        ["docker", "inspect", "--format={{.State.StartedAt}}", name],
        capture_output=True, text=True
    )
    started = result.stdout.strip()
    if not started:
        return ""
    from datetime import datetime, timezone
    try:
        started = started.split(".")[0]
        dt = datetime.fromisoformat(started).replace(tzinfo=timezone.utc)
        now = datetime.now(timezone.utc)
        delta = now - dt
        hours, remainder = divmod(int(delta.total_seconds()), 3600)
        minutes, _ = divmod(remainder, 60)
        if hours >= 24:
            return f"{hours // 24}d {hours % 24}h"
        if hours > 0:
            return f"{hours}h {minutes}m"
        return f"{minutes}m"
    except (ValueError, TypeError):
        return ""


def wait_healthy(container: str, label: str, timeout: int = 60) -> bool:
    """Wait for container to become healthy. Returns success."""
    if not is_container_running(container):
        UI.step_fail(f"{label} (not running)")
        return False

    UI.step(label)
    for _ in range(timeout):
        result = subprocess.run(
            ["docker", "inspect", "--format={{.State.Health.Status}}", container],
            capture_output=True, text=True
        )
        status = result.stdout.strip()
        if status == "healthy":
            UI.step_ok(label)
            return True
        elif status == "unhealthy":
            UI.step_fail(f"{label} (unhealthy)")
            return False
        time.sleep(1)
    UI.step_fail(f"{label} (timeout {timeout}s)")
    return False


def compose_args(*extra_files: Path) -> list[str]:
    """Build docker compose -f arguments, merging overlays."""
    args = ["-f", str(COMPOSE_FILE)]
    for f in extra_files:
        if f and f.exists():
            args += ["-f", str(f)]
    return args


def run_compose(*docker_args, check=True, env=None, **kwargs):
    """Run docker compose with configured compose files. Includes logging overlay."""
    cmd = ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + list(docker_args)
    merged = {**os.environ, **(env or {})}
    result = subprocess.run(cmd, env=merged, **kwargs)
    if check and result.returncode != 0:
        UI.err(f"Command failed: {' '.join(cmd)}")
        if result.stderr:
            UI.err(result.stderr.strip())
        sys.exit(1)
    return result


def run_compose_test(*docker_args, check=True, **kwargs):
    cmd = ["docker", "compose", "-f", str(TEST_COMPOSE_FILE)] + list(docker_args)
    result = subprocess.run(cmd, **kwargs)
    if check and result.returncode != 0:
        UI.err(f"Test compose failed: {' '.join(cmd)}")
        sys.exit(1)
    return result
