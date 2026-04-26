"""IP Address Management for ipvlan_docker network."""

import json
import os
import subprocess
from typing import Optional

from dev.core import (
    IP_ALLOC_FILE, IPVLAN_NETWORK, IPVLAN_SERVICES, UI, get_env
)


# ===========================================
# IP conversion
# ===========================================
def ip_to_int(ip: str) -> int:
    parts = ip.strip().split(".")
    if len(parts) != 4:
        raise ValueError(f"Invalid IP: {ip}")
    return (int(parts[0]) << 24) | (int(parts[1]) << 16) | (int(parts[2]) << 8) | int(parts[3])


def int_to_ip(n: int) -> str:
    return f"{(n >> 24) & 0xFF}.{(n >> 16) & 0xFF}.{(n >> 8) & 0xFF}.{n & 0xFF}"


# ===========================================
# Persistence
# ===========================================
def load_ip_allocs() -> dict:
    if IP_ALLOC_FILE.exists():
        try:
            return json.loads(IP_ALLOC_FILE.read_text())
        except (json.JSONDecodeError, IOError):
            return {}
    return {}


def save_ip_allocs(allocs: dict):
    IP_ALLOC_FILE.parent.mkdir(parents=True, exist_ok=True)
    IP_ALLOC_FILE.write_text(json.dumps(allocs, indent=2) + "\n")


# ===========================================
# Discovery
# ===========================================
def get_reserved_ips() -> set:
    """Parse IPVLAN_RESERVED from env: e.g. '172.18.0.1-172.18.0.50' or comma-sep IPs."""
    reserved = set()
    raw = get_env("IPVLAN_RESERVED", "")
    if not raw:
        return reserved
    for part in raw.split(","):
        part = part.strip()
        if "-" in part:
            start_s, end_s = part.split("-", 1)
            try:
                start = ip_to_int(start_s.strip())
                end = ip_to_int(end_s.strip())
                for ip_int in range(start, end + 1):
                    reserved.add(int_to_ip(ip_int))
            except ValueError:
                pass
        else:
            reserved.add(part)
    return reserved


def get_docker_used_ips() -> set:
    """Get IPs currently in use on ipvlan_docker."""
    used = set()
    result = subprocess.run(
        ["docker", "network", "inspect", "-f",
         "{{range .Containers}}{{.IPv4Address}} {{end}}", IPVLAN_NETWORK],
        capture_output=True, text=True
    )
    if result.returncode == 0:
        for entry in result.stdout.split():
            ip = entry.split("/")[0]
            if ip:
                used.add(ip)
    return used


def find_free_ip(start: str = "172.18.0.51", max_attempts: int = 1000) -> Optional[str]:
    """Find an unused IP, avoiding reserved and docker-used addresses."""
    reserved = get_reserved_ips()
    used = get_docker_used_ips()
    start_int = ip_to_int(start)
    for offset in range(max_attempts):
        ip = int_to_ip(start_int + offset)
        if ip not in reserved and ip not in used:
            return ip
    return None


# ===========================================
# Allocation orchestration
# ===========================================
def ensure_ips_allocated() -> dict:
    """Ensure all services have an IP. Returns {service: ip}.

    Tracks both docker-used IPs and IPs already assigned in this run
    to prevent allocating the same address to multiple services.
    """
    allocs = load_ip_allocs()
    used = get_docker_used_ips()
    assigned_this_run: set = set()
    updated = False

    for svc in IPVLAN_SERVICES:
        ip = allocs.get(svc, "")
        if ip and ip not in used and ip not in assigned_this_run:
            assigned_this_run.add(ip)
            continue

        # Scan for IP that isn't reserved, docker-used, or already assigned
        reserved = get_reserved_ips()
        start_int = ip_to_int("172.18.0.51")
        free: Optional[str] = None
        for offset in range(1000):
            candidate = int_to_ip(start_int + offset)
            if candidate not in reserved and candidate not in used and candidate not in assigned_this_run:
                free = candidate
                break

        if free:
            allocs[svc] = free
            assigned_this_run.add(free)
            updated = True
        else:
            UI.warn(f"No free IP for {svc} (pool exhausted)")
            allocs[svc] = ""
            updated = True
        save_ip_allocs(allocs)

    return allocs


def ip_env_for_services() -> dict:
    """Return {IPVLAN_IP_<SERVICE>: ip} for use in compose."""
    allocs = load_ip_allocs()
    return {f"IPVLAN_IP_{svc.upper()}": allocs.get(svc, "") for svc in IPVLAN_SERVICES}


def detect_ip_conflicts(allocs: dict) -> list[str]:
    """Check for duplicate or docker-conflicting IPs. Returns list of issues."""
    issues = []
    ips = list(allocs.values())
    if len(ips) != len(set(ips)):
        dupes = {ip for ip in ips if ips.count(ip) > 1}
        issues.append(f"Duplicate IPs: {dupes}")
    used = get_docker_used_ips()
    conflicts = set(ips) & used
    if conflicts:
        issues.append(f"IPs already in use: {conflicts}")
    return issues
