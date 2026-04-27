"""Xray proxy management.

Note: xray commands use the root docker-compose.yml (not dev.docker-compose.yml)
because Xray/Nginx are production proxy services, not part of the dev environment.
"""

import json
import subprocess

from dev.paths import INSTANCES_JSON, GENERATOR_SCRIPT, PROJECT_ROOT
from dev.core import UI, check_deps, get_ipvlan_network


def cmd_generate(args):
    """Generate Xray configs from templates."""
    if not GENERATOR_SCRIPT.exists():
        UI.die(f"Generator script not found: {GENERATOR_SCRIPT}")
    UI.ok("Generating Xray instance configs...")
    result = subprocess.run(
        ["python3", str(GENERATOR_SCRIPT)],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        UI.err("Generation failed")
        return 1
    UI.ok("Generation complete")
    return 0


def cmd_list_instances(args):
    """List configured Xray instances."""
    if not INSTANCES_JSON.exists():
        UI.info("No instances generated. Run: dev.py generate")
        return 0
    data = json.loads(INSTANCES_JSON.read_text())
    print()
    UI.info("Xray Proxy Instances:")
    print()
    for inst in data:
        print(f"  {inst['name']}: {inst['xray_name']} ({inst['xray_path']})")
    print()
    print(f"  {UI.DIM}Details: .local/instances.json (keep private){UI.NC}")
    return 0


def cmd_xray_start(args):
    """Start Xray proxy services (uses root docker-compose.yml)."""
    check_deps()
    network = get_ipvlan_network()
    result = subprocess.run(
        ["docker", "network", "ls", "--format", "{{.Name}}"],
        capture_output=True, text=True
    )
    if network not in result.stdout:
        UI.warn(f"Network {network} not found")
        UI.info("Run: sudo bash setup-networks.sh")
        return 1
    UI.ok("Starting Xray instances...")
    result = subprocess.run(
        ["docker", "compose", "up", "-d"],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        UI.err("Failed to start")
        return 1
    UI.ok("Xray instances started")
    return 0


def cmd_xray_stop(args):
    """Stop Xray proxy services (uses root docker-compose.yml)."""
    UI.ok("Stopping Xray instances...")
    result = subprocess.run(
        ["docker", "compose", "stop"],
        cwd=str(PROJECT_ROOT)
    )
    if result.returncode != 0:
        UI.err("Failed to stop")
        return 1
    UI.ok("Stopped")
    return 0
