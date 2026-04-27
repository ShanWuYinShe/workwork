"""Configuration commands."""

import os
import subprocess

from dev.paths import ENV_FILE, IP_ALLOC_FILE, BACKUP_DIR, PROJECT_ROOT
from dev.core import UI, NC, load_env, get_ipvlan_services
from dev.config import get_config
from dev.ipam import load_ip_allocs, get_docker_used_ips


def cmd_config(args):
    """View or edit dev-env configuration."""
    load_env()
    sub = getattr(args, 'sub', None)

    if sub == "show" or sub is None:
        UI.info("Current Configuration:")
        print()
        print(f"  {UI.BOLD}Project:{NC} {get_config('project', PROJECT_ROOT.name)}")
        print(f"  {UI.BOLD}Root:{NC}   {PROJECT_ROOT}")
        print(f"  {UI.BOLD}Network:{NC} {get_config('network.name', 'ipvlan_docker')}")
        print(f"  {UI.BOLD}Reserved:{NC} {get_config('network.reserved', '(none)')}")
        print()
        print(f"  {UI.BOLD}Ports:{NC}")
        print(f"    PostgreSQL  : {get_config('services.postgres.port', 5433)}")
        print(f"    Redis       : {get_config('services.redis.port', 6379)}")
        print(f"    MinIO API   : {get_config('services.minio.api_port', 9002)}")
        print(f"    MinIO Console: {get_config('services.minio.console_port', 9003)}")
        print()
        print(f"  {UI.BOLD}Databases:{NC}")
        print(f"    PostgreSQL  : {get_config('services.postgres.db', 'app')} / {get_config('services.postgres.user', 'app')}")
        print(f"    MinIO       : {get_config('services.minio.user', 'minioadmin')}")
        print()
        print(f"  {UI.BOLD}Files:{NC}")
        print(f"    Config      : dev.yml")
        print(f"    Override    : {ENV_FILE}")
        print(f"    IP allocs   : {IP_ALLOC_FILE}")
        print(f"    Backups     : {BACKUP_DIR}")
        return 0

    if sub == "edit":
        editor = os.environ.get("EDITOR", "vi")
        target = ENV_FILE if ENV_FILE.exists() else PROJECT_ROOT / "dev.yml"
        subprocess.run([editor, str(target)])
        return 0

    if sub == "ip":
        allocs = load_ip_allocs()
        UI.info("IP Allocations:")
        used_ips = get_docker_used_ips()
        for svc in get_ipvlan_services():
            ip = allocs.get(svc, "(unassigned)")
            status = f"{UI.GREEN}(in use){NC}" if ip in used_ips else ""
            print(f"  {svc:<10} {ip}  {status}")
        return 0
