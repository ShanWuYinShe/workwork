"""System commands: doctor, prune, version, completion, port-usage, dashboard, init, status."""

import json
import os
import subprocess
import sys

from dev.paths import (
    VERSION, COMPOSE_FILE, TEST_COMPOSE_FILE, ENV_FILE, CONFIG_FILE,
    BACKUP_DIR, SNAPSHOT_DIR, PROJECT_ROOT, CERT_DIR,
    LOGGING_OVERLAY,
)
from dev.core import (
    UI, NC, load_env, check_deps, is_container_running,
    run_compose, compose_args, get_ipvlan_services, get_ipvlan_network,
    project_prefix, container_health, container_uptime,
)
import dev.core as _core
from dev.config import get_config, validate_config


def cmd_init(args):
    """Initialize the development environment."""
    load_env()
    UI.header("dev-env Init")

    if ENV_FILE.exists():
        UI.info(".env already exists")
    else:
        UI.info(f"Config: {CONFIG_FILE}")
        if CONFIG_FILE.exists():
            UI.ok("dev.yml found (committed defaults)")
        UI.info("Create .env for local overrides if needed")

    network = get_ipvlan_network()
    result = subprocess.run(
        ["docker", "network", "ls", "--format", "{{.Name}}"],
        capture_output=True, text=True
    )
    if network in result.stdout:
        UI.ok(f"Network {network} exists")
    else:
        UI.warn(f"Network {network} not found")
        UI.info("Run: sudo bash setup-networks.sh")

    if CERT_DIR.exists() and any(CERT_DIR.iterdir()):
        UI.ok("Certs found in cert/")
    else:
        UI.warn("No TLS certificates found in cert/")
        UI.info("Place server_cert.pem and server_cert.key in cert/")

    check_deps()
    UI.ok("Docker ready")
    UI.ok("Initialization complete")
    return 0


def cmd_status(args):
    """Show environment status."""
    load_env()
    if _core.JSON_OUTPUT:
        _status_json()
        return 0
    print()
    UI.info("Dev Environment Status")
    print()
    run_compose("ps", "--format", "table {{.Name}}\t{{.Status}}\t{{.Ports}}")
    print()
    prefix = project_prefix()
    print(f"{UI.BOLD}Port Mappings:{NC}")
    print(f"  PostgreSQL     : localhost:{get_config('services.postgres.port', 5433)}")
    print(f"  Redis          : localhost:{get_config('services.redis.port', 6379)}")
    print(f"  MinIO API      : localhost:{get_config('services.minio.api_port', 9002)}")
    print(f"  MinIO Console  : localhost:{get_config('services.minio.console_port', 9003)}")
    print()
    print(f"{UI.BOLD}Container Env Vars:{NC}")
    print(f"  POSTGRES_HOST  = {prefix}-postgres")
    print(f"  REDIS_HOST     = {prefix}-redis")
    print(f"  MINIO_ENDPOINT = {prefix}-minio:9000")
    return 0


def _status_json():
    """Output status as JSON."""
    prefix = project_prefix()
    services = []
    for svc in get_ipvlan_services():
        name = f"{prefix}-{svc}"
        running = is_container_running(name)
        services.append({
            "name": svc,
            "container": name,
            "running": running,
            "health": container_health(name) or None if running else None,
        })
    print(json.dumps({"project": prefix, "services": services}, indent=2))


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

    # 2. Config Files
    UI.header("2. Config Files")
    config_issues = validate_config()
    if config_issues:
        for issue in config_issues:
            print(f"  {UI.RED}✗{NC} {issue}")
            errors += 1
    else:
        print(f"  {UI.GREEN}✓{NC} Configuration valid")

    for f, label in [(CONFIG_FILE, "dev.yml"),
                     (COMPOSE_FILE, "dev.docker-compose.yml"),
                     (TEST_COMPOSE_FILE, "dev-test.yml")]:
        if not f.exists():
            print(f"  {UI.RED}✗{NC} {label} (not found)")
            errors += 1
            continue
        if f.suffix == ".yml" and f.name != "dev.yml":
            r = subprocess.run(["docker", "compose", "-f", str(f), "config", "--quiet"],
                              capture_output=True)
            if r.returncode == 0:
                print(f"  {UI.GREEN}✓{NC} {label} (valid)")
            else:
                print(f"  {UI.RED}✗{NC} {label} (syntax error)")
                errors += 1
        else:
            print(f"  {UI.GREEN}✓{NC} {label}")

    # 3. Environment
    UI.header("3. Environment")
    if ENV_FILE.exists():
        print(f"  {UI.GREEN}✓{NC} .env exists")
    else:
        print(f"  {UI.DIM}⋅{NC} .env not found (using dev.yml defaults)")

    # 4. Ports
    UI.header("4. Port Availability")
    ports = [
        ("PostgreSQL", get_config("services.postgres.port", "5433")),
        ("Redis", get_config("services.redis.port", "6379")),
        ("MinIO", get_config("services.minio.api_port", "9002")),
    ]
    for svc, port in ports:
        r = subprocess.run(
            ["lsof", "-i", f":{port}", "-sTCP:LISTEN", "-n", "-P"],
            capture_output=True, text=True
        )
        if r.stdout.strip():
            print(f"  {UI.YELLOW}⚠{NC} {svc} ({port}) in use")
        else:
            print(f"  {UI.GREEN}✓{NC} {svc} ({port}) available")

    # 5. Running Services
    UI.header("5. Running Services")
    prefix = project_prefix()
    for svc in [f"{prefix}-{s}" for s in get_ipvlan_services()]:
        if is_container_running(svc):
            health = container_health(svc)
            uptime = container_uptime(svc)
            h = f" ({health})" if health else ""
            u = f" up {uptime}" if uptime else ""
            print(f"  {UI.GREEN}✓{NC} {svc}{h}{u}")
        else:
            print(f"  {UI.DIM}⋅{NC} {svc} (not running)")

    print()
    if errors == 0:
        UI.ok("Doctor: no issues found")
    else:
        UI.err(f"Doctor: {errors} issue(s) found")
    return 1 if errors else 0


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
    return 0


def cmd_completion(args):
    """Generate shell completion scripts."""
    shell = getattr(args, 'shell', None) or "bash"

    # Get command list from dispatch keys (avoids argparse internals)
    from dev.cli import _DISPATCH_KEYS
    cmds = " ".join(_DISPATCH_KEYS)

    if shell == "bash":
        print(f'''_dev_py_complete() {{
    local cur prev words cword
    _init_completion || return
    local cmds="{cmds}"
    local services="postgres redis minio dev"
    local shells="fish bash zsh sh"
    if [[ $cword -eq 1 ]]; then
        COMPREPLY=($(compgen -W "$cmds" -- "$cur"))
    elif [[ "$prev" == "logs" || "$prev" == "restart" ]]; then
        COMPREPLY=($(compgen -W "$services" -- "$cur"))
    elif [[ "$prev" == "shell" ]]; then
        COMPREPLY=($(compgen -W "$shells" -- "$cur"))
    fi
}}
complete -F _dev_py_complete dev.py''')
    elif shell == "zsh":
        cmds_zsh = cmds.replace(" ", " ")
        print(f'''#compdef dev.py
_dev_py() {{
    local -a cmds services shells
    cmds=({cmds_zsh})
    services=(postgres redis minio dev)
    shells=(fish bash zsh sh)
    _arguments \\
        "1:command:($cmds)" \\
        "2:subcmd:->subcmd"
    case $words[2] in
        logs|restart) _describe 'service' services ;;
        shell) _describe 'shell' shells ;;
    esac
}}
_dev_py''')
    else:
        UI.warn(f"Completion for {shell} not supported (use bash or zsh)")
    return 0


def cmd_version(args):
    """Show version."""
    print(f"{UI.CYAN}dev-env v{VERSION}{NC}")
    return 0


def cmd_port_usage(args):
    """Show which process is using a port."""
    load_env()
    port = getattr(args, 'port', None)
    if port:
        if not port.isdigit() or not (1 <= int(port) <= 65535):
            UI.die(f"Invalid port number: {port} (must be 1-65535)")
    if not port:
        UI.info("Common port usage:")
        common = [
            (get_config("services.postgres.port", "5433"), "PostgreSQL"),
            (get_config("services.redis.port", "6379"), "Redis"),
            (get_config("services.minio.api_port", "9002"), "MinIO API"),
            (get_config("services.minio.console_port", "9003"), "MinIO Console"),
            ("3000", "Dev (Next.js)"),
            ("3001", "Dev (API)"),
            ("8080", "Dev (Go)"),
        ]
        for p, label in common:
            r = subprocess.run(
                ["lsof", "-i", f":{p}", "-sTCP:LISTEN", "-n", "-P"],
                capture_output=True, text=True
            )
            lines = [l for l in r.stdout.strip().splitlines() if not l.startswith("COMMAND")]
            if lines:
                proc = lines[0].split()[0]
                print(f"  {UI.CYAN}{p}{NC}  {label}  {UI.DIM}({proc}){NC}")
            else:
                print(f"  {UI.DIM}{p}  {label}  (available){NC}")
        return 0

    r = subprocess.run(
        ["lsof", "-i", f":{port}", "-sTCP:LISTEN", "-n", "-P"],
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
    return 0


def cmd_dashboard(args=None):
    """Interactive dashboard shown when dev.py is run with no args."""
    load_env()
    prefix = project_prefix()

    from dev.ipam import load_ip_allocs
    allocs = load_ip_allocs()

    print()
    print(f"{UI.BOLD}{UI.CYAN}dev-env v{VERSION}{NC}  {UI.DIM}{PROJECT_ROOT.name}{NC}")
    print()

    services = get_ipvlan_services()
    running = []
    stopped = []
    for svc in services:
        cname = f"{prefix}-{svc}"
        if is_container_running(cname):
            health = container_health(cname)
            uptime = container_uptime(cname)
            running.append((svc, cname, allocs.get(svc, "N/A"), health, uptime))
        else:
            stopped.append((svc, cname))

    if running:
        print(f"  {UI.BOLD}{UI.GREEN}● Running{NC}")
        for svc, cname, ip, health, uptime in running:
            h = f" {UI.DIM}({health}){NC}" if health and health != "healthy" else ""
            u = f" {UI.DIM}up {uptime}{NC}" if uptime else ""
            print(f"    {UI.GREEN}✓{NC} {svc:<10} {cname}  {UI.DIM}{ip}{NC}{h}{u}")
    else:
        print(f"  {UI.DIM}  No services running (dev.py start){NC}")

    if stopped:
        print(f"  {UI.BOLD}{UI.RED}● Stopped{NC}")
        for svc, cname in stopped:
            print(f"    {UI.RED}⋅{NC} {svc:<10} {cname}")

    # Show last backup
    if BACKUP_DIR.exists():
        backups = sorted(BACKUP_DIR.glob("backup_*_postgres.sql"), reverse=True)
        if backups:
            ts = backups[0].name.replace("backup_", "").replace("_postgres.sql", "")
            date = f"{ts[0:4]}-{ts[4:6]}-{ts[6:8]} {ts[9:11]}:{ts[11:13]}:{ts[13:15]}"
            print(f"\n  {UI.DIM}Last backup: {date}{NC}")

    print()
    print(f"  {UI.BOLD}Commands:{NC}")
    print(f"    {UI.DIM}up      down    build   ps      shell   logs{NC}")
    print(f"    {UI.DIM}start   stop    restart doctor  backup  version{NC}")
    print()
    return 0
