"""Backup and snapshot management."""

import subprocess
import sys
import time
from pathlib import Path

from dev.paths import BACKUP_DIR, SNAPSHOT_DIR, LOGGING_OVERLAY
from dev.core import UI, NC, load_env, compose_args, project_prefix, is_container_running
from dev.config import get_config


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
    tmp_file = pg_file.parent / f".{pg_file.name}.tmp"
    pg_cmd = ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
        "exec", "-T", "postgres", "pg_dump",
        "-U", get_config("services.postgres.user", "app"),
        "-d", get_config("services.postgres.db", "app")
    ]
    with open(tmp_file, "w") as f:
        result = subprocess.run(pg_cmd, stdout=f)
    if result.returncode == 0:
        tmp_file.rename(pg_file)
        UI.info(f"PostgreSQL: {pg_file.stat().st_size // 1024}KB")
    else:
        tmp_file.unlink(missing_ok=True)
        UI.warn("PostgreSQL: backup failed")
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
    return 1 if failed else 0


def cmd_list_backups(args):
    """List available backups."""
    load_env()
    if not BACKUP_DIR.exists():
        UI.info("No backups")
        return 0
    backups = sorted(BACKUP_DIR.glob("backup_*_postgres.sql"), reverse=True)
    if not backups:
        UI.info("No backups")
        return 0
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
        print(f"    Restore: dev.py restore .local/backups/{ts}")
    print()
    return 0


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
        UI.die(f"Backup files not found: {Path(target).name}")

    # Check if services are running before attempting restore
    prefix = project_prefix()
    if pg_sql.exists() and not is_container_running(f"{prefix}-postgres"):
        UI.die("PostgreSQL not running. Run: dev.py start")
    if rd_tar.exists() and not is_container_running(f"{prefix}-redis"):
        UI.die("Redis not running. Run: dev.py start")

    failed = 0
    if pg_sql.exists():
        UI.ok("Restoring PostgreSQL...")
        with open(pg_sql) as f:
            result = subprocess.run(
                ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
                    "exec", "-T", "postgres", "psql",
                    "-U", get_config("services.postgres.user", "app"),
                    "-d", get_config("services.postgres.db", "app")
                ], stdin=f
            )
        if result.returncode == 0:
            UI.info("PostgreSQL: restored")
        else:
            UI.warn("PostgreSQL: restore failed")
            failed += 1

    if rd_tar.exists():
        UI.ok("Restoring Redis...")
        rd_vol = f"{prefix}_rd_data"
        # Clean data dir first
        subprocess.run([
            "docker", "run", "--rm",
            "-v", f"{rd_vol}:/data",
            "alpine", "sh", "-c", "rm -rf /data/*"
        ])
        # Extract backup
        result = subprocess.run([
            "docker", "run", "--rm",
            "-v", f"{rd_vol}:/data",
            "-v", f"{BACKUP_DIR}:/backup",
            "alpine", "tar", "xzf", f"/backup/{rd_tar.name}", "-C", "/data"
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
    return 1 if failed else 0


def cmd_snapshot(args):
    """Manage PostgreSQL named snapshots."""
    load_env()
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
        return 0

    if sub_cmd == "create":
        name = getattr(args, 'name', None) or time.strftime("%Y%m%d_%H%M%S")
        snap_file = SNAPSHOT_DIR / f"{name}.sql"
        tmp_file = snap_file.parent / f".{snap_file.name}.tmp"
        UI.ok(f"Creating snapshot: {name}")
        with open(tmp_file, "w") as f:
            result = subprocess.run(
                ["docker", "compose"] + compose_args(LOGGING_OVERLAY) + [
                    "exec", "-T", "postgres", "pg_dump",
                    "-U", get_config("services.postgres.user", "app"),
                    "-d", get_config("services.postgres.db", "app")
                ], stdout=f
            )
        if result.returncode == 0:
            tmp_file.rename(snap_file)
            UI.ok(f"Snapshot: {name} ({snap_file.stat().st_size // 1024}KB)")
        else:
            tmp_file.unlink(missing_ok=True)
            UI.err("Snapshot failed")
            return 1
        return 0

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
                    "-U", get_config("services.postgres.user", "app"),
                    "-d", get_config("services.postgres.db", "app")
                ], stdin=f
            )
        if result.returncode == 0:
            UI.ok("Snapshot restored")
            return 0
        else:
            UI.err("Restore failed")
            return 1

    if sub_cmd == "delete":
        name = getattr(args, 'name', None)
        if not name:
            UI.die("Usage: dev.py snapshot delete <name>")
        snap_file = SNAPSHOT_DIR / f"{name}.sql"
        if not snap_file.exists():
            UI.die(f"Snapshot not found: {name}")
        snap_file.unlink()
        UI.ok(f"Deleted: {name}")
        return 0
