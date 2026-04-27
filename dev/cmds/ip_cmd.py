"""IP address management commands."""

import subprocess

from dev.core import UI, NC, load_env, get_ipvlan_services, get_ipvlan_network, project_prefix
from dev.config import get_config
from dev.ipam import load_ip_allocs, save_ip_allocs, find_free_ip, get_docker_used_ips, get_reserved_ips, ip_to_int


def cmd_ip(args):
    """Manage IP allocations."""
    load_env()
    sub = getattr(args, 'sub', None)
    services = get_ipvlan_services()
    network = get_ipvlan_network()

    if sub == "list" or sub is None:
        UI.info(f"Network: {network}")
        reserved_raw = get_config("network.reserved", "")
        if reserved_raw:
            print(f"  Reserved: {reserved_raw}")
        used_docker = get_docker_used_ips()
        allocs = load_ip_allocs()
        print()
        print(f"{UI.BOLD}Allocations:{NC}")
        if not allocs:
            print(f"  {UI.DIM}No allocations (run dev.py start){NC}")
        else:
            for svc in services:
                ip = allocs.get(svc, "")
                status = ""
                if ip in used_docker:
                    status = f"{UI.GREEN} (in use){NC}"
                elif ip:
                    status = f"{UI.YELLOW} (unused){NC}"
                mark = f"{UI.GREEN}*{NC}" if ip in used_docker else f"{UI.DIM} {NC}"
                print(f"  {mark} {svc:<12} {ip or '(unassigned)'} {status}")
        return 0

    elif sub == "alloc":
        svc = getattr(args, 'service', None)
        ip = getattr(args, 'ip', None)
        if not svc:
            UI.die("Usage: dev.py ip alloc <service> [ip]")
        if svc not in services:
            UI.die(f"Unknown: {svc}. Options: {', '.join(services)}")
        allocs = load_ip_allocs()
        if ip:
            parts = ip.split(".")
            if len(parts) != 4 or not all(p.isdigit() and 0 <= int(p) <= 255 for p in parts):
                UI.die(f"Invalid IP address: {ip}")
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
        return 0

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
        return 0

    elif sub == "used":
        UI.info(f"IPs in use on {network}:")
        used_ips = get_docker_used_ips()
        if not used_ips:
            print(f"  {UI.DIM}No containers connected{NC}")
        else:
            result = subprocess.run(
                ["docker", "network", "inspect", "-f",
                 "{{range .Containers}}{{.Name}}={{.IPv4Address}} {{end}}", network],
                capture_output=True, text=True
            )
            for entry in result.stdout.split():
                if "=" in entry:
                    name, addr = entry.split("=", 1)
                    ip = addr.split("/")[0]
                    print(f"  {name:<30} {ip}")
        return 0
    else:
        UI.die(f"Unknown subcommand: {sub}")
