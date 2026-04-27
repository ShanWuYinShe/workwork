"""Test environment management."""

import subprocess

from dev.paths import TEST_COMPOSE_FILE
from dev.core import UI, check_deps, wait_healthy, run_compose_test


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
    return 1 if failed else 0


def cmd_stop_test(args):
    """Stop and destroy test environment."""
    UI.ok("Stopping test environment...")
    run_compose_test("down", "-v")
    UI.ok("Cleaned up")
    return 0


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
    return 1 if failed else 0


def cmd_test(args):
    """Full test environment teardown and restart."""
    rc = cmd_stop_test(args)
    if rc != 0:
        return rc
    return cmd_start_test(args)
