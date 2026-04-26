"""dev-env core infrastructure: paths, UI, environment, Docker helpers."""

import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Optional

# ===========================================
# Paths & Constants
# ===========================================
SCRIPT_DIR = Path(__file__).parent
PROJECT_ROOT = SCRIPT_DIR.parent
COMPOSE_FILE = PROJECT_ROOT / "dev.docker-compose.yml"
TEST_COMPOSE_FILE = SCRIPT_DIR / "dev-test.yml"
LOGGING_OVERLAY = SCRIPT_DIR / "logging.yml"
BACKUP_DIR = PROJECT_ROOT / ".local" / "backups"
SNAPSHOT_DIR = PROJECT_ROOT / ".local" / "snapshots"
INSTANCES_CONF = PROJECT_ROOT / "instances.conf"
INSTANCES_JSON = PROJECT_ROOT / ".local" / "instances.json"
CONFIG_DIR = PROJECT_ROOT / "configs"
TEMPLATES_DIR = PROJECT_ROOT / "templates"
IP_ALLOC_FILE = PROJECT_ROOT / ".local" / "ip-allocations.json"
ENV_FILE = PROJECT_ROOT / ".env"
ENV_TEMPLATE = SCRIPT_DIR / ".env.template"
GENERATOR_SCRIPT = PROJECT_ROOT / "scripts" / "generate_instances.py"

# Shorthand color constant for use in f-strings across modules
NC = "\033[0m"
IPVLAN_NETWORK = "ipvlan_docker"
IPVLAN_SUBNET = "172.18.0.0/16"
IPVLAN_SERVICES = ["postgres", "redis", "minio", "dev"]
VERSION = "4.2.0"

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
        print(f"{cls.GREEN}[OK]{cls.NC} {msg}")

    @classmethod
    def info(cls, msg: str):
        print(f"{cls.BLUE}[INFO]{cls.NC} {msg}")

    @classmethod
    def warn(cls, msg: str):
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
        print(f"    {msg}...", end="", flush=True)

    @classmethod
    def step_ok(cls, msg: str):
        print(f"\r    {cls.GREEN}✓{cls.NC} {msg}")

    @classmethod
    def step_fail(cls, msg: str):
        print(f"\r    {cls.RED}✗{cls.NC} {msg}")

    @classmethod
    def header(cls, text: str):
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
    """Load .env file into os.environ. Returns current environ snapshot.

    Pre-sets IPVLAN_IP_* vars to suppress compose warnings when a service
    hasn't been allocated an IP yet.
    """
    for var in ["IPVLAN_IP_POSTGRES", "IPVLAN_IP_REDIS", "IPVLAN_IP_MINIO", "IPVLAN_IP_DEV"]:
        os.environ.setdefault(var, "")
    if not ENV_FILE.exists():
        return dict(os.environ)
    for line in ENV_FILE.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        os.environ[key.strip()] = value.strip()
    return dict(os.environ)


def get_env(key: str, default: str = "") -> str:
    return os.environ.get(key, default)


def project_prefix() -> str:
    return get_env("COMPOSE_PROJECT", "service")


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
