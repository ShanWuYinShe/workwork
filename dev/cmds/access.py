"""Container access: shell, exec, run, logs, wait."""

import subprocess
import sys

from dev.core import (
    UI, load_env, is_container_running, wait_healthy,
    run_compose, compose_args, LOGGING_OVERLAY,
    project_prefix,
)


def cmd_shell(args):
    """Open a shell inside the dev container."""
    load_env()
    prefix = project_prefix()
    container = f"{prefix}-dev"
    if not is_container_running(container):
        UI.die("Dev container not running. Run: dev.py start")
    shell = getattr(args, 'shell', None) or "fish"
    subprocess.run(["docker", "exec", "-it", container, shell])
    return 0


def cmd_exec(args):
    """Run a command inside the dev container."""
    load_env()
    prefix = project_prefix()
    container = f"{prefix}-dev"
    if not is_container_running(container):
        UI.die("Dev container not running. Run: dev.py start")
    exec_args = getattr(args, 'args', [])
    if not exec_args:
        UI.die("Usage: dev.py exec <command...>")
    cmd = ["docker", "exec"]
    if sys.stdin.isatty():
        cmd += ["-it", container]
    else:
        cmd.append(container)
    cmd += exec_args
    return subprocess.run(cmd).returncode


def cmd_run(args):
    """Run a one-off command in the dev container (non-interactive)."""
    load_env()
    prefix = project_prefix()
    container = f"{prefix}-dev"
    if not is_container_running(container):
        UI.die("Dev container not running. Run: dev.py start")
    cmd_args = getattr(args, 'args', [])
    if not cmd_args:
        UI.die("Usage: dev.py run <command...>")
    return subprocess.run(["docker", "exec", container] + cmd_args).returncode


def cmd_logs(args):
    """Tail logs from services."""
    load_env()
    svc = getattr(args, 'service', None) or ""
    tail = getattr(args, 'tail', None) or "50"
    cmd = ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + ["logs", "-f", "--tail", tail]
    if svc:
        cmd.append(svc)
    return subprocess.run(cmd).returncode


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
        return 1
    UI.ok("All services healthy")
    return 0
